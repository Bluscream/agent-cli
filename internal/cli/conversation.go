package cli

import (
	"fmt"
	"strings"

	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

func conversationCommand(o *options) *cobra.Command {
	var targetProvider string
	var recoverFlag bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:     "conversation [conversation-id]",
		Aliases: []string{"convo", "convos", "chat", "chats"},
		Short:   "Inspect detailed conversation metadata, artifacts, workspace git status, or trigger recovery",
		RunE: func(cmd *cobra.Command, args []string) error {
			provName := targetProvider
			if provName == "" {
				provName = o.provider
			}

			// Handle --recover flag
			if recoverFlag {
				var p provider.Provider
				var err error
				if provName != "" {
					p, err = provider.Get(provName)
				} else {
					p, err = provider.Get("antigravity")
				}
				if err != nil {
					return err
				}

				reportStr, err := p.RecoverConversations(dryRun)
				if err != nil {
					return fmt.Errorf("recovery failed: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), reportStr)
				return nil
			}

			if len(args) == 0 {
				return fmt.Errorf("conversation ID is required (or specify --recover)")
			}
			convoID := args[0]

			var detail *provider.ConversationDetail

			if provName != "" {
				p, err := provider.Get(provName)
				if err != nil {
					return err
				}
				d, err := p.GetConversation(convoID)
				if err != nil {
					return err
				}
				detail = d
			} else {
				// Search across all providers
				for _, p := range provider.All() {
					d, e := p.GetConversation(convoID)
					if e == nil && d != nil {
						detail = d
						break
					}
				}
			}

			if detail == nil {
				return fmt.Errorf("conversation not found: %s", convoID)
			}
			o.timer.Step("conversation_fetched")

			if o.isJSON() {
				return o.printJSON(cmd.OutOrStdout(), detail)
			}

			// 1. Details Table
			dt := o.newDetail(cmd.OutOrStdout())
			detailRows(dt,
				kv("Provider", detail.Summary.Provider),
				kv("Short ID", idutil.ShortID(detail.Summary.ID)),
				kv("Full Raw ID", detail.Summary.ID),
				kv("Title", detail.Summary.Title),
				kv("Created", detail.Summary.CreatedAt.Format("2006-01-02 15:04:05")),
				kv("Last Modified", detail.Summary.UpdatedAt.Format("2006-01-02 15:04:05")),
				kv("Model", detail.Summary.Model),
				kv("Workspace Path", detail.Summary.WorkspaceDir),
				kv("Transcript Path", detail.TranscriptPath),
			)
			fmt.Fprintln(cmd.OutOrStdout(), o.renderTable(dt))

			// 2. Git Status Section
			if detail.WorkspaceGit.IsRepo {
				fmt.Fprintln(cmd.OutOrStdout(), bold.Sprint("\nWORKSPACE GIT REPOSITORY STATUS:"))
				gt := o.newDetail(cmd.OutOrStdout())
				dirtyStr := colorStatus("clean")
				if detail.WorkspaceGit.IsDirty {
					dirtyStr = colorStatus("dirty")
				}
				detailRows(gt,
					kv("Work Dir", detail.WorkspaceGit.WorkDir),
					kv("Branch", detail.WorkspaceGit.Branch),
					kv("Latest Commit", fmt.Sprintf("%s (%s)", detail.WorkspaceGit.CommitSHA, detail.WorkspaceGit.CommitMsg)),
					kv("Dirty State", dirtyStr),
					kv("Modified Files", fmt.Sprintf("%d", detail.WorkspaceGit.ModifiedCount)),
					kv("Untracked Files", fmt.Sprintf("%d", detail.WorkspaceGit.UntrackedCount)),
					kv("Staged Files", fmt.Sprintf("%d", detail.WorkspaceGit.StagedCount)),
					kv("Diff Summary", detail.WorkspaceGit.DiffStat),
				)
				fmt.Fprintln(cmd.OutOrStdout(), o.renderTable(gt))
			}

			// 3. Artifacts Table
			if len(detail.Artifacts) > 0 {
				fmt.Fprintln(cmd.OutOrStdout(), bold.Sprintf("\nGENERATED ARTIFACTS (%d):", len(detail.Artifacts)))
				at := o.newTable(cmd.OutOrStdout())
				at.AppendHeader(table.Row{"Name", "Type", "Size", "Modified", "Path"})
				// Fixed cols: Name (~20) + Type (~12) + Size (~10) + Modified (~18) + borders (~18) = ~78
				at.SetColumnConfigs([]table.ColumnConfig{
					o.flexColConfig(cmd.OutOrStdout(), 5, 78),
				})
				for _, a := range detail.Artifacts {
					at.AppendRow(table.Row{
						a.Name,
						a.Type,
						o.sizeCell(a.SizeBytes),
						a.ModifiedAt.Format("2006-01-02 15:04"),
						a.Path,
					})
				}
				fmt.Fprintln(cmd.OutOrStdout(), o.renderTable(at))
			}

			// 4. Initial Prompt & Last Response
			if detail.InitialPrompt != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n%s\n", bold.Sprint("INITIAL USER PROMPT:"), strings.TrimSpace(detail.InitialPrompt))
			}
			if detail.LastResponse != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n%s\n", bold.Sprint("LAST AGENT RESPONSE:"), strings.TrimSpace(detail.LastResponse))
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&targetProvider, "provider", "p", "", "Target provider")
	cmd.Flags().BoolVar(&recoverFlag, "recover", false, "Scan disk for unindexed or corrupted conversations and recover them")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Perform scan and show recoverable sessions without modifying files")

	return cmd
}
