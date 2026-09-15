package cli

import (
	"fmt"
	"strings"

	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
	"github.com/spf13/cobra"
)

func handoffCommand(o *options) *cobra.Command {
	var targetProvider string
	var lastOnly bool

	cmd := &cobra.Command{
		Use:     "handoff [conversation-id]",
		Aliases: []string{"audit"},
		Short:   "Produce a self-contained briefing dossier of a conversation for cross-agent auditing and handoff",
		Long: `Produce a complete, structured briefing dossier of a conversation so any agent or script
can immediately inspect and finish off work without needing to do manual file digging.

Example:
  ai handoff --provider claude --last
  ai handoff <id-or-short-id>
  ai handoff --provider codex --last --output json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			provName := targetProvider
			if provName == "" {
				provName = o.provider
			}

			var targetID string
			if len(args) > 0 {
				targetID = args[0]
			}

			var p provider.Provider
			var err error
			if provName != "" {
				p, err = provider.Get(provName)
				if err != nil {
					return err
				}
			} else {
				// Pick provider with the most recent activity
				var mostRecent provider.Provider
				var latestTime int64
				for _, prov := range provider.All() {
					convos, err := prov.ListConversations(provider.HistoryOptions{Last: true})
					if err == nil && len(convos) > 0 {
						if convos[0].UpdatedAt.Unix() > latestTime {
							latestTime = convos[0].UpdatedAt.Unix()
							mostRecent = prov
						}
					}
				}
				if mostRecent == nil {
					return fmt.Errorf("no conversations found to audit")
				}
				p = mostRecent
			}
			o.timer.Step("provider_resolved")

			dossier, err := p.AuditConversation(targetID)
			if err != nil {
				return fmt.Errorf("audit failed: %w", err)
			}
			o.timer.Step("conversation_audited")

			if o.isJSON() {
				return o.printJSON(cmd.OutOrStdout(), dossier)
			}

			// Render clean Markdown / Terminal dossier
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, bold.Sprintf("=== AGENT HANDOFF DOSSIER: %s [%s] ===", dossier.Title, strings.ToUpper(dossier.Provider)))
			fmt.Fprintf(out, "Short ID        : %s\n", idutil.ShortID(dossier.ConversationID))
			fmt.Fprintf(out, "Conversation ID : %s\n", dossier.ConversationID)
			fmt.Fprintf(out, "Last Activity   : %s\n", dossier.LastActive.Format("2006-01-02 15:04:05"))
			fmt.Fprintf(out, "Workspace Dir   : %s\n", dossier.WorkspaceDir)
			if dossier.FullTranscriptPath != "" {
				fmt.Fprintf(out, "Transcript Log  : %s\n", dossier.FullTranscriptPath)
			}

			fmt.Fprintln(out, bold.Sprint("\n[1] WORKSPACE GIT STATE:"))
			if dossier.WorkspaceGit.IsRepo {
				dirtyTag := colorStatus("clean")
				if dossier.WorkspaceGit.IsDirty {
					dirtyTag = colorStatus("dirty")
				}
				fmt.Fprintf(out, "  Branch        : %s\n", dossier.WorkspaceGit.Branch)
				fmt.Fprintf(out, "  Latest Commit : %s - %s\n", dossier.WorkspaceGit.CommitSHA, dossier.WorkspaceGit.CommitMsg)
				fmt.Fprintf(out, "  Status        : %s (modified: %d, untracked: %d, staged: %d)\n",
					dirtyTag, dossier.WorkspaceGit.ModifiedCount, dossier.WorkspaceGit.UntrackedCount, dossier.WorkspaceGit.StagedCount)
				if dossier.WorkspaceGit.DiffStat != "" {
					fmt.Fprintf(out, "  Diff Stat     : %s\n", dossier.WorkspaceGit.DiffStat)
				}
				if len(dossier.WorkspaceGit.StatusLines) > 0 {
					fmt.Fprintln(out, "  Modified Files:")
					maxLines := 10
					for i, l := range dossier.WorkspaceGit.StatusLines {
						if i >= maxLines {
							fmt.Fprintf(out, "    ... and %d more files\n", len(dossier.WorkspaceGit.StatusLines)-maxLines)
							break
						}
						fmt.Fprintf(out, "    %s\n", l)
					}
				}
			} else {
				fmt.Fprintln(out, "  (Directory is not a git repository)")
			}

			fmt.Fprintln(out, bold.Sprint("\n[2] USER GOAL / LAST INSTRUCTION:"))
			if dossier.UserPrompt != "" {
				fmt.Fprintf(out, "%s\n", strings.TrimSpace(dossier.UserPrompt))
			} else {
				fmt.Fprintln(out, "  (No prompt captured)")
			}

			fmt.Fprintln(out, bold.Sprint("\n[3] LAST AGENT RESPONSE / PROPOSALS:"))
			if dossier.AssistantSummary != "" {
				fmt.Fprintf(out, "%s\n", strings.TrimSpace(dossier.AssistantSummary))
			} else {
				fmt.Fprintln(out, "  (No response recorded)")
			}

			if len(dossier.PendingOpenTasks) > 0 {
				fmt.Fprintln(out, bold.Sprintf("\n[4] DETECTED OPEN TASKS (%d):", len(dossier.PendingOpenTasks)))
				for _, task := range dossier.PendingOpenTasks {
					fmt.Fprintf(out, "  - [ ] %s\n", task)
				}
			}

			if len(dossier.TouchedArtifacts) > 0 {
				fmt.Fprintln(out, bold.Sprintf("\n[5] TOUCHED ARTIFACTS (%d):", len(dossier.TouchedArtifacts)))
				for _, art := range dossier.TouchedArtifacts {
					fmt.Fprintf(out, "  • %s (%s, %s)\n", art.Name, o.sizeCell(art.SizeBytes), art.Path)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&targetProvider, "provider", "p", "", "Target provider (antigravity, claude, codex)")
	cmd.Flags().BoolVar(&lastOnly, "last", true, "Target the latest conversation")

	return cmd
}
