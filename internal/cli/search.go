package cli

import (
	"fmt"
	"strings"

	"agentcli.local/ai/internal/search"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

func searchCommand(o *options) *cobra.Command {
	var (
		queryText       string
		queryPattern    string
		targetWorkspace string
		targetProvider  string
		typeFilter      string
		limit           int
		caseSensitive   bool
	)

	cmd := &cobra.Command{
		Use:   "search [QUERY]",
		Short: "Search across conversations, memories, and skills",
		Long: `Search across conversations, transcripts, memories, and skills across all AI providers.
Attribution reports where, when, and by whom a topic was mentioned with surrounding context.

Examples:
  ai search "pkg-manager"
  ai search --text "steam-cli" --workspace "/run/media/system/Data/Projects"
  ai search --pattern "git\\s+(commit|push)"
  ai search --text "api key" --type memories
  ai search --text "docker" --output json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && queryText == "" && queryPattern == "" {
				queryText = strings.Join(args, " ")
			}

			if queryText == "" && queryPattern == "" {
				return fmt.Errorf("search query required: pass as an argument, or via --text / --pattern")
			}

			filterProv := targetProvider
			if filterProv == "" {
				filterProv = o.provider
			}

			results, err := search.Execute(search.Options{
				Text:          queryText,
				Pattern:       queryPattern,
				Workspace:     targetWorkspace,
				Provider:      filterProv,
				TypeFilter:    typeFilter,
				Limit:         limit,
				CaseSensitive: caseSensitive,
			})
			if err != nil {
				return err
			}

			if o.isJSON() {
				return o.printJSON(cmd.OutOrStdout(), results)
			}

			out := cmd.OutOrStdout()
			if len(results) == 0 {
				fmt.Fprintln(out, "No matching results found.")
				return nil
			}

			t := o.newTable(out)
			t.AppendHeader(table.Row{"TYPE", "PROVIDER", "ID", "WHO", "WHEN", "LOCATION", "CONTEXT / MATCH"})

			// Base overhead for fixed columns and borders:
			// Col 1 (TYPE): ~14, Col 2 (PROVIDER): ~13, Col 3 (ID): ~10,
			// Col 4 (WHO): ~11, Col 5 (WHEN): ~18, Borders (8 vertical pipes): 8,
			// Cell padding for cols 6 & 7: 4.
			// Total base overhead: 78.
			t.SetColumnConfigs(o.distributeFlexCols(out, 78,
				FlexColSpec{Number: 6, MinWidth: 15, Ratio: 3}, // LOCATION
				FlexColSpec{Number: 7, MinWidth: 25, Ratio: 7}, // CONTEXT / MATCH
			))

			for _, r := range results {
				whoStr := r.Author
				switch strings.ToLower(r.Author) {
				case "user":
					whoStr = bold.Sprint("USER")
				case "assistant", "model":
					whoStr = cyan.Sprint("ASSISTANT")
				case "thinking":
					whoStr = faint("thinking")
				case "memory":
					whoStr = yellow.Sprint("MEMORY")
				case "skill":
					whoStr = green.Sprint("SKILL")
				default:
					whoStr = strings.ToUpper(r.Author)
				}

				locStr := r.Location
				if r.EntityTitle != "" && r.Type == "conversation" {
					locStr = fmt.Sprintf("%s (%s)", r.Location, r.EntityTitle)
				}

				t.AppendRow(table.Row{
					strings.ToUpper(r.Type),
					r.Provider,
					r.EntityID,
					whoStr,
					o.dateTimeCell(r.Timestamp),
					locStr,
					r.Snippet,
				})
			}

			fmt.Fprintln(out, o.renderTable(t))
			return nil
		},
	}

	cmd.Flags().StringVarP(&queryText, "text", "t", "", "Literal text search")
	cmd.Flags().StringVarP(&queryPattern, "pattern", "e", "", "Regular expression pattern search")
	cmd.Flags().StringVarP(&targetWorkspace, "workspace", "w", "", "Filter conversations by workspace path")
	cmd.Flags().StringVarP(&targetProvider, "provider", "p", "", "Filter by provider (antigravity, claude, codex)")
	cmd.Flags().StringVar(&typeFilter, "type", "", "Filter by entity type (conversations, memories, skills)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 25, "Maximum number of results to display")
	cmd.Flags().BoolVarP(&caseSensitive, "case-sensitive", "s", false, "Enable case-sensitive matching")

	return cmd
}
