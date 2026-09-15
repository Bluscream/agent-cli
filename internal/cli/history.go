package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

func historyCommand(o *options) *cobra.Command {
	var sinceStr string
	var lastOnly bool
	var limit int
	var targetProvider string
	var targetWorkspace string

	cmd := &cobra.Command{
		Use:   "history",
		Short: "List and search conversations across all agent providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			var sinceTime time.Time
			if sinceStr != "" {
				t, err := ParseSinceDuration(sinceStr)
				if err != nil {
					return err
				}
				sinceTime = t
			}

			histOpts := provider.HistoryOptions{
				Since:     sinceTime,
				Last:      lastOnly || o.last,
				Limit:     limit,
				Workspace: targetWorkspace,
			}

			if targetWorkspace != "" {
				histOpts.Limit = 0
				histOpts.Last = false
			}

			var provList []provider.Provider
			filterProv := targetProvider
			if filterProv == "" {
				filterProv = o.provider
			}

			if filterProv != "" {
				p, err := provider.Get(filterProv)
				if err != nil {
					return err
				}
				provList = []provider.Provider{p}
			} else {
				provList = provider.All()
			}

			var allConvos []provider.ConversationSummary
			for _, p := range provList {
				convos, err := p.ListConversations(histOpts)
				if err == nil {
					allConvos = append(allConvos, convos...)
				}
			}
			o.timer.Step("conversations_queried")

			// Filter by workspace if specified
			if targetWorkspace != "" {
				cleanTarget := strings.TrimPrefix(targetWorkspace, "file://")
				cleanTarget = filepath.Clean(cleanTarget)

				var filtered []provider.ConversationSummary
				for _, c := range allConvos {
					ws := strings.TrimPrefix(c.WorkspaceDir, "file://")
					ws = filepath.Clean(ws)
					if ws == "." || ws == "" {
						continue
					}
					// Check if workspace matches or is a parent/child
					// "only shows conversations that happened in this workspace or any parent"
					// Meaning: ws is cleanTarget OR cleanTarget has prefix ws OR ws has prefix cleanTarget
					if ws == cleanTarget || strings.HasPrefix(cleanTarget, ws+string(filepath.Separator)) || strings.HasPrefix(ws, cleanTarget+string(filepath.Separator)) {
						filtered = append(filtered, c)
					}
				}
				allConvos = filtered
			}

			// Sort unified across providers
			sort.Slice(allConvos, func(i, j int) bool {
				return allConvos[i].UpdatedAt.After(allConvos[j].UpdatedAt)
			})

			if (lastOnly || o.last) && len(allConvos) > 0 {
				allConvos = allConvos[:1]
			} else if limit > 0 && len(allConvos) > limit {
				allConvos = allConvos[:limit]
			}
			o.timer.Step("conversations_sorted_filtered")

			if o.isJSON() {
				return o.printJSON(cmd.OutOrStdout(), allConvos)
			}

			out := cmd.OutOrStdout()
			t := o.newTable(out)
			t.AppendHeader(table.Row{"Provider", "ID", "Title", "Workspace", "Messages", "Artifacts", "Total Size", "Created", "Last Modified"})

			// Base overhead for fixed columns and borders:
			// Col 1 (Provider): ~13, Col 2 (ID): ~10, Col 5 (Messages): ~10,
			// Col 6 (Artifacts): ~11, Col 7 (Total Size): ~12, Col 8 (Created): ~18,
			// Col 9 (Last Mod): ~18, Borders (10 vertical pipes): 10, Cell padding for cols 3 & 4: 4.
			// Total base overhead: 106
			const baseOverhead = 106
			titleWidth, wsWidth := 30, 30
			if cols := termWidth(out); cols > 0 {
				rem := cols - baseOverhead
				if rem >= 20 {
					titleWidth = (rem * 6) / 10
					wsWidth = rem - titleWidth
				} else {
					titleWidth = 10
					wsWidth = 10
				}
			}
			t.SetColumnConfigs([]table.ColumnConfig{
				o.flexCol(3, titleWidth),
				o.flexCol(4, wsWidth),
			})

			for _, c := range allConvos {
				wsDisplay := strings.TrimPrefix(c.WorkspaceDir, "file://")
				createdStr := c.CreatedAt.Format("2006-01-02 15:04")
				updatedStr := c.UpdatedAt.Format("2006-01-02 15:04")

				t.AppendRow(table.Row{
					c.Provider,
					idutil.ShortID(c.ID),
					c.Title,
					wsDisplay,
					c.MessagesCount,
					c.ArtifactsCount,
					o.sizeCell(c.TotalSizeBytes),
					createdStr,
					updatedStr,
				})
			}

			fmt.Fprintln(cmd.OutOrStdout(), o.renderTable(t))
			return nil
		},
	}

	cmd.Flags().StringVarP(&targetProvider, "provider", "p", "", "Filter by provider (antigravity, claude, codex)")
	cmd.Flags().StringVar(&sinceStr, "since", "", "Filter conversations since duration (e.g. 2w, 1d, 3h)")
	cmd.Flags().BoolVar(&lastOnly, "last", false, "Show only the single most recent conversation")
	cmd.Flags().IntVarP(&limit, "limit", "n", 25, "Maximum number of conversations to display")
	cmd.Flags().StringVarP(&targetWorkspace, "workspace", "w", "", "Filter conversations that occurred in this workspace or any parent directory")

	return cmd
}
