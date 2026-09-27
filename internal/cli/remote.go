package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/ingest"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// remoteFlagUsage is shared so the three commands describe --remote identically.
const remoteFlagUsage = "Read from the ingested Qdrant collection instead of local transcript files"

func addRemoteFlag(cmd *cobra.Command, target *bool) {
	cmd.Flags().BoolVar(target, "remote", false, remoteFlagUsage)
}

// renderRemoteSearch prints remote matches in the same shape as a local
// search, so switching to --remote does not change how results read.
func renderRemoteSearch(o *options, out io.Writer, matches []ingest.RemoteMatch, unique bool) error {
	if unique {
		seen := map[string]struct{}{}
		var deduped []ingest.RemoteMatch
		for _, m := range matches {
			if _, dup := seen[m.SessionID]; dup {
				continue
			}
			seen[m.SessionID] = struct{}{}
			deduped = append(deduped, m)
		}
		matches = deduped
	}

	if o.isJSON() {
		return o.printJSON(out, matches)
	}
	if len(matches) == 0 {
		fmt.Fprintln(out, "No matching results found in the ingested collection.")
		return nil
	}

	t := o.newTable(out)
	t.AppendHeader(table.Row{"PROVIDER", "ID", "WHO", "WHEN", "LOCATION", "CONTEXT / MATCH"})
	// Fixed columns: provider ~13, id ~10, who ~11, when ~18, borders 7, padding 4.
	t.SetColumnConfigs(o.distributeFlexCols(out, 63,
		FlexColSpec{Number: 5, MinWidth: 15, Ratio: 3},
		FlexColSpec{Number: 6, MinWidth: 25, Ratio: 7},
	))

	for _, m := range matches {
		location := fmt.Sprintf("Step #%d", m.StepIndex)
		if m.Title != "" {
			location = fmt.Sprintf("%s (%s)", location, m.Title)
		}
		t.AppendRow(table.Row{
			m.Provider,
			idutil.ShortID(m.SessionID),
			roleCell(m.Role),
			o.dateTimeCell(m.CreatedAt),
			location,
			truncateCell(m.Content, 400),
		})
	}

	fmt.Fprintln(out, o.renderTable(t))
	return nil
}

func roleCell(role string) string {
	switch strings.ToLower(role) {
	case "user":
		return bold.Sprint("USER")
	case "assistant", "model":
		return cyan.Sprint("ASSISTANT")
	case "thinking":
		return faint("thinking")
	default:
		return strings.ToUpper(role)
	}
}

// remoteConfig resolves the ingestion destination for a --remote query,
// translating an unset or broken configuration into an error that says what to
// do about it rather than a bare failure.
func remoteConfig() (*ingest.Config, error) {
	cfg, err := ingest.LoadConfig()
	if errors.Is(err, ingest.ErrNotConfigured) {
		return nil, fmt.Errorf(
			"--remote needs an ingested collection, but %s is not set.\n"+
				"Set it (see `ai ingest --help`) and run `ai ingest` to populate the collection,\n"+
				"or drop --remote to read the local transcript files",
			ingest.EnvQdrantURL)
	}
	if err != nil {
		return nil, err
	}
	return cfg, nil
}
