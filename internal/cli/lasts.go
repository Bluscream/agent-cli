package cli

import (
	"fmt"
	"sort"
	"strings"

	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
	"github.com/spf13/cobra"
)

func lastsCommand(o *options) *cobra.Command {
	var targetProvider string
	var limit int

	cmd := &cobra.Command{
		Use:   "lasts",
		Short: "Display rich details for the last N conversations including first prompt and last response",
		Long: `Display rich, multi-field briefing cards for the most recent N conversations across
providers, showing each conversation's initial user instruction, last agent response,
workspace info, and metadata so you can quickly find and resume work.

Examples:
  ai lasts
  ai lasts -n 10
  ai lasts --provider claude`,
		RunE: func(cmd *cobra.Command, args []string) error {
			provName := targetProvider
			if provName == "" {
				provName = o.provider
			}

			var provList []provider.Provider
			if provName != "" {
				p, err := provider.Get(provName)
				if err != nil {
					return err
				}
				provList = []provider.Provider{p}
			} else {
				provList = provider.All()
			}

			var allConvos []provider.ConversationSummary
			for _, p := range provList {
				convos, err := p.ListConversations(provider.HistoryOptions{Limit: limit * 2})
				if err == nil {
					allConvos = append(allConvos, convos...)
				}
			}

			sort.Slice(allConvos, func(i, j int) bool {
				return allConvos[i].UpdatedAt.After(allConvos[j].UpdatedAt)
			})

			if limit <= 0 {
				limit = 5
			}
			if len(allConvos) > limit {
				allConvos = allConvos[:limit]
			}

			if len(allConvos) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No conversations found.")
				return nil
			}

			type RichLastEntry struct {
				Summary       provider.ConversationSummary `json:"summary"`
				InitialPrompt string                       `json:"initial_prompt"`
				LastResponse  string                       `json:"last_response"`
				GitBranch     string                       `json:"git_branch,omitempty"`
				GitDirty      bool                         `json:"git_dirty"`
			}

			var entries []RichLastEntry
			for _, c := range allConvos {
				p, err := provider.Get(c.Provider)
				if err != nil {
					continue
				}
				detail, err := p.GetConversation(c.ID)
				if err != nil || detail == nil {
					continue
				}
				entries = append(entries, RichLastEntry{
					Summary:       c,
					InitialPrompt: detail.InitialPrompt,
					LastResponse:  detail.LastResponse,
					GitBranch:     detail.WorkspaceGit.Branch,
					GitDirty:      detail.WorkspaceGit.IsDirty,
				})
			}

			if o.isJSON() {
				return o.printJSON(cmd.OutOrStdout(), entries)
			}

			out := cmd.OutOrStdout()
			for i, e := range entries {
				c := e.Summary
				shortID := idutil.ShortID(c.ID)

				// Header bar
				fmt.Fprintf(out, "\n%s #%d [%s] %s  (Short ID: %s)\n",
					bold.Sprint("═══ CONVERSATION"),
					i+1,
					cyan.Sprint(strings.ToUpper(c.Provider)),
					bold.Sprint(shortID),
					shortID,
				)

				// Details table
				dt := o.newDetail(out)
				detailRows(dt,
					kv("Title", c.Title),
					kv("Short ID", shortID),
					kv("Full Raw ID", c.ID),
					kv("Last Active", c.UpdatedAt.Format("2006-01-02 15:04:05")),
					kv("Workspace", c.WorkspaceDir),
					kv("Stats", fmt.Sprintf("%d messages, %d artifacts, %s", c.MessagesCount, c.ArtifactsCount, o.sizeCell(c.TotalSizeBytes))),
				)
				if e.GitBranch != "" {
					dirtyStr := colorStatus("clean")
					if e.GitDirty {
						dirtyStr = colorStatus("dirty")
					}
					detailRows(dt, kv("Git Repo", fmt.Sprintf("branch %s (%s)", e.GitBranch, dirtyStr)))
				}
				fmt.Fprintln(out, o.renderTable(dt))

				// Initial prompt
				if e.InitialPrompt != "" {
					promptPreview := strings.TrimSpace(e.InitialPrompt)
					if len(promptPreview) > 300 {
						promptPreview = promptPreview[:300] + "..."
					}
					fmt.Fprintf(out, "\n%s\n%s\n", bold.Sprint("  INITIAL USER PROMPT:"), promptPreview)
				}

				// Last response
				if e.LastResponse != "" {
					respPreview := strings.TrimSpace(e.LastResponse)
					if len(respPreview) > 300 {
						respPreview = respPreview[:300] + "..."
					}
					fmt.Fprintf(out, "\n%s\n%s\n", bold.Sprint("  LAST AGENT RESPONSE:"), respPreview)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&targetProvider, "provider", "p", "", "Filter by provider")
	cmd.Flags().IntVarP(&limit, "limit", "n", 5, "Number of most recent conversations to display")

	return cmd
}
