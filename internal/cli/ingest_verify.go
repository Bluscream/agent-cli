package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"agentcli.local/ai/internal/ingest"
)

func ingestVerifyCommand(o *options) *cobra.Command {
	var (
		targetProvider string
		sinceStr       string
		deep           bool
	)

	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check that every local conversation is stored in the collection and readable back",
		Long: `Compare each conversation on this machine against what the collection holds.

Every conversation is counted separately rather than checking the collection
total, because a total can match while individual conversations are missing —
one over-published and one absent cancel out.

--deep additionally reads the stored turns back and compares their text, which
is what confirms a conversation is retrievable rather than merely counted. It
costs one scroll per conversation.

Nothing is written. A session stored remotely with no local conversation is
reported as an orphan.

Examples:
  ai ingest verify                    # count check across every provider
  ai ingest verify --deep             # also read the stored text back
  ai ingest verify -p claude          # one provider only
  ai ingest verify --since 2w         # only recently updated conversations`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ingest.LoadConfig()
			if errors.Is(err, ingest.ErrNotConfigured) {
				return reportNotConfigured(o, cmd.OutOrStdout())
			}
			if err != nil {
				return err
			}

			opts := ingest.VerifyOptions{Provider: targetProvider, Deep: deep}
			if opts.Provider == "" {
				opts.Provider = o.provider
			}
			if sinceStr != "" {
				since, err := ParseSinceDuration(sinceStr)
				if err != nil {
					return fmt.Errorf("invalid --since value: %w", err)
				}
				opts.Since = since
			}

			report, err := ingest.Verify(cmd.Context(), cfg, opts)
			if err != nil {
				return err
			}
			if err := renderVerifyReport(o, cmd.OutOrStdout(), report); err != nil {
				return err
			}
			// A nonzero exit so this is usable as a check in a script, rather
			// than something whose output has to be read to learn it failed.
			if !report.Healthy() {
				return errors.New("the collection does not match this machine's conversations")
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&targetProvider, "provider", "p", "", "Only verify one provider (antigravity, claude, codex)")
	cmd.Flags().StringVar(&sinceStr, "since", "", "Only verify conversations updated since duration (e.g. 2w, 1d, 3h)")
	cmd.Flags().BoolVar(&deep, "deep", false, "Read stored turns back and compare their text, not just their count")
	return cmd
}

func renderVerifyReport(o *options, out io.Writer, report *ingest.VerifyReport) error {
	if o.isJSON() {
		return o.printJSON(out, report)
	}

	dt := o.newDetail(out)
	rows := [][2]string{
		kv("Endpoint", report.Endpoint),
		kv("Collection", report.Collection),
		kv("Mode", verifyMode(report.Deep)),
		kv("Conversations (local)", fmt.Sprintf("%d", report.Local)),
		kv("Verified", green.Sprintf("%d", report.OK)),
		kv("Empty (nothing to store)", fmt.Sprintf("%d", report.Empty)),
		kv("Missing", countOrDash(report.Missing, red)),
		kv("Incomplete", countOrDash(report.Incomplete, yellow)),
		kv("Stale", countOrDash(report.Stale, yellow)),
		kv("Failed to check", countOrDash(report.Failed, red)),
		kv("Points expected", fmt.Sprintf("%d", report.ExpectedPoints)),
		kv("Points stored", fmt.Sprintf("%d", report.StoredPoints)),
		kv("Points in collection", fmt.Sprintf("%d", report.TotalPoints)),
	}
	if report.RemoteSessions >= 0 {
		rows = append(rows, kv("Sessions in collection", fmt.Sprintf("%d", report.RemoteSessions)))
		rows = append(rows, kv("Orphaned sessions", countOrDash(len(report.Orphans), yellow)))
	}
	rows = append(rows, kv("Duration", report.Duration))
	detailRows(dt, rows...)
	fmt.Fprintln(out, o.renderTable(dt))

	if len(report.Issues) > 0 {
		tbl := o.newTable(out)
		tbl.AppendHeader(table.Row{"Provider", "ID", "Status", "Expected", "Stored", "Detail"})
		for _, issue := range report.Issues {
			detail := issue.Error
			if detail == "" && issue.MissingContent > 0 {
				detail = fmt.Sprintf("%d turns differ or are absent", issue.MissingContent)
			}
			tbl.AppendRow(table.Row{
				issue.Provider,
				issue.ID,
				string(issue.Status),
				issue.Expected,
				issue.Stored,
				detail,
			})
		}
		fmt.Fprintln(out, o.renderTable(tbl))
		fmt.Fprintln(out, "Re-publish these with: ai ingest --force")
	}

	if len(report.Orphans) > 0 {
		fmt.Fprintln(out, yellow.Sprintf(
			"%d stored session(s) no longer exist on this machine; remove them with: ai ingest --cleanup",
			len(report.Orphans)))
	}
	if report.Healthy() {
		fmt.Fprintln(out, green.Sprint("Every local conversation is stored and readable back."))
	}
	return nil
}

func verifyMode(deep bool) string {
	if deep {
		return "deep (counts and stored text)"
	}
	return "counts only (--deep also compares text)"
}

// countOrDash keeps a healthy report quiet: a zero problem count reads as "-"
// rather than a coloured zero competing for attention.
func countOrDash(n int, colour interface{ Sprintf(string, ...any) string }) string {
	if n == 0 {
		return "-"
	}
	return colour.Sprintf("%d", n)
}
