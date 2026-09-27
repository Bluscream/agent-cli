package cli

import (
	"errors"
	"fmt"
	"io"
	"time"

	"agentcli.local/ai/internal/ingest"
	"github.com/spf13/cobra"
)

// notConfiguredMessage explains how to switch ingestion on rather than failing
// with a bare error, because an unset destination is a normal state.
func notConfiguredMessage() string {
	return fmt.Sprintf(`Ingestion is not configured.

Set %s to a Qdrant endpoint to enable it, for example in
~/.config/environment.d/30-ai-ingest.conf:

  %s=http://qdrant.example:6333
  %s=%s

Optional: %s, %s, %s.`,
		ingest.EnvQdrantURL,
		ingest.EnvQdrantURL,
		ingest.EnvCollection, ingest.DefaultCollection,
		ingest.EnvQdrantAPIKey, ingest.EnvHostname, ingest.EnvOffsetsFile)
}

func ingestCommand(o *options) *cobra.Command {
	var (
		targetProvider string
		sinceStr       string
		force          bool
		dryRun         bool
		watch          bool
		debounceStr    string
	)

	cmd := &cobra.Command{
		Use:   "ingest",
		Short: "Publish conversation transcripts to a Qdrant collection for off-machine search",
		Long: fmt.Sprintf(`Publish conversation transcripts from every provider into a Qdrant collection.

Ingestion only runs when %s is set; without it this command explains what to
set and exits successfully. Conversations unchanged since the last pass are
skipped, so repeated runs are cheap.

Points are payload-only: no embedding model is involved and no vectors are
written. Search the result with Qdrant's payload filters.

Examples:
  ai ingest                      # publish everything that changed
  ai ingest --dry-run            # report what would be published
  ai ingest --provider claude    # one provider only
  ai ingest --since 2w           # only recently updated conversations
  ai ingest --force              # republish regardless of the offset store
  ai ingest --watch              # keep running and publish as transcripts change
  ai ingest status               # destination health and local offset state`,
			ingest.EnvQdrantURL),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ingest.LoadConfig()
			if errors.Is(err, ingest.ErrNotConfigured) {
				return reportNotConfigured(o, cmd.OutOrStdout())
			}
			if err != nil {
				return err
			}

			opts := ingest.Options{Provider: targetProvider, Force: force, DryRun: dryRun}
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
			var progress *ingestProgress
			if !o.isJSON() {
				progress = newIngestProgress(cmd.OutOrStdout(), dryRun)
				opts.OnProgress = progress.Handle
			}

			if watch {
				debounce, err := time.ParseDuration(debounceStr)
				if err != nil {
					return fmt.Errorf("invalid --debounce value: %w", err)
				}
				return runIngestWatch(cmd, o, cfg, opts, debounce)
			}

			result, err := ingest.Run(cmd.Context(), cfg, opts)
			if progress != nil {
				progress.Finish()
			}
			if err != nil {
				return err
			}
			return renderIngestResult(o, cmd.OutOrStdout(), result)
		},
	}

	cmd.Flags().StringVarP(&targetProvider, "provider", "p", "", "Only ingest one provider (antigravity, claude, codex)")
	cmd.Flags().StringVar(&sinceStr, "since", "", "Only ingest conversations updated since duration (e.g. 2w, 1d, 3h)")
	cmd.Flags().BoolVar(&force, "force", false, "Republish conversations even when unchanged since the last pass")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Report what would be published without writing anything")
	cmd.Flags().BoolVar(&watch, "watch", false, "Keep running, publishing transcripts as they change")
	cmd.Flags().StringVar(&debounceStr, "debounce", "5s", "How long to wait for writes to settle before ingesting in --watch")

	cmd.AddCommand(ingestStatusCommand(o))
	return cmd
}

// runIngestWatch ingests once and then on every transcript change until the
// command's context is cancelled, which cobra ties to SIGINT/SIGTERM.
func runIngestWatch(cmd *cobra.Command, o *options, cfg *ingest.Config, opts ingest.Options, debounce time.Duration) error {
	if o.isJSON() {
		return errors.New("--watch streams continuously and has no JSON form; drop --output or drop --watch")
	}

	watcher, err := ingest.NewWatcher(cfg, opts, debounce)
	if err != nil {
		return err
	}
	defer watcher.Close()

	out := cmd.OutOrStdout()
	roots, _ := ingest.WatchRoots(opts.Provider)
	fmt.Fprintf(out, "Watching %d transcript directories, settling for %s before each pass.\n", len(roots), debounce)
	for _, root := range roots {
		fmt.Fprintf(out, "  %s\n", root)
	}
	fmt.Fprintln(out, "Press Ctrl-C to stop.")

	watcher.OnPass = func(result *ingest.Result, err error) {
		stamp := time.Now().Format("15:04:05")
		if err != nil {
			fmt.Fprintf(out, "[%s] %s\n", stamp, red.Sprint(err))
			return
		}
		if result.Published == 0 && result.Failed == 0 {
			return
		}
		fmt.Fprintf(out, "[%s] published %d conversations (%d points), %d failed\n",
			stamp, result.Published, result.Points, result.Failed)
	}

	if err := watcher.Run(cmd.Context()); err != nil {
		return err
	}
	fmt.Fprintln(out, "Stopped.")
	return nil
}

func reportNotConfigured(o *options, out io.Writer) error {
	if o.isJSON() {
		return o.printJSON(out, ingest.Status{Configured: false})
	}
	fmt.Fprintln(out, notConfiguredMessage())
	return nil
}

func renderIngestResult(o *options, out io.Writer, result *ingest.Result) error {
	if o.isJSON() {
		return o.printJSON(out, result)
	}

	heading := "INGESTED"
	if result.DryRun {
		heading = "DRY RUN — NOTHING WRITTEN"
	}
	fmt.Fprintf(out, "\n%s\n", bold.Sprint(heading))

	failedCell := fmt.Sprintf("%d", result.Failed)
	if result.Failed > 0 {
		failedCell = red.Sprint(failedCell)
	}

	publishedLabel, pointsLabel := "Published", "Points sent"
	if result.DryRun {
		publishedLabel, pointsLabel = "Would publish", "Points to send"
	}

	rows := [][2]string{
		kv("Destination", fmt.Sprintf("%s at %s", result.Collection, result.Endpoint)),
		kv("Scanned", formatCount(result.Conversations)),
		kv(publishedLabel, formatCount(result.Published)),
		kv("Skipped (unchanged)", formatCount(result.Skipped)),
		kv("Failed", failedCell),
		kv(pointsLabel, formatCount(result.Points)),
		kv("Duration", result.Duration),
	}
	if before, after := result.PointsBefore, result.PointsAfter; before >= 0 && after >= 0 {
		delta, _ := result.Delta()
		rows = append(rows, kv("Collection points",
			fmt.Sprintf("%s → %s (%s)", formatCount(before), formatCount(after), signed(delta))))
	}

	dt := o.newDetail(out)
	detailRows(dt, rows...)
	fmt.Fprintln(out, o.renderTable(dt))

	if result.Failed > 0 {
		return fmt.Errorf("%d conversations could not be ingested", result.Failed)
	}
	return nil
}

// signed renders a delta with an explicit sign so "no change" is unambiguous.
func signed(n int64) string {
	if n > 0 {
		return "+" + formatCount(n)
	}
	if n == 0 {
		return "no change"
	}
	return formatCount(n)
}

func ingestStatusCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the ingestion destination's health and local offset state",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ingest.LoadConfig()
			if errors.Is(err, ingest.ErrNotConfigured) {
				return reportNotConfigured(o, cmd.OutOrStdout())
			}
			if err != nil {
				return err
			}

			status := ingest.GetStatus(cmd.Context(), cfg)
			if o.isJSON() {
				return o.printJSON(cmd.OutOrStdout(), status)
			}

			health := status.Health
			if !status.Reachable {
				health = red.Sprint("unreachable")
			} else if status.Health == "absent" {
				health = yellow.Sprint("not created yet")
			}

			dt := o.newDetail(cmd.OutOrStdout())
			detailRows(dt,
				kv("Endpoint", status.Endpoint),
				kv("Collection", status.Collection),
				kv("Health", health),
				kv("Points (remote)", fmt.Sprintf("%d", status.RemotePoints)),
				kv("Conversations (tracked)", fmt.Sprintf("%d", status.TrackedLocal)),
				kv("Points (tracked)", fmt.Sprintf("%d", status.TrackedPoints)),
				kv("Last ingest", orDash(status.LastIngestAt)),
				kv("Offsets file", status.OffsetsFile),
			)
			fmt.Fprintln(cmd.OutOrStdout(), o.renderTable(dt))

			if status.Error != "" {
				fmt.Fprintln(cmd.OutOrStdout(), red.Sprintf("Error: %s", status.Error))
				return errors.New("ingestion destination is not reachable")
			}
			return nil
		},
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
