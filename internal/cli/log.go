package cli

import (
	"fmt"
	"strings"

	"agentcli.local/ai/internal/provider"
	"github.com/spf13/cobra"
)

func logCommand(o *options) *cobra.Command {
	var targetProvider string
	var showTools bool
	var showThinking bool
	var noErrors bool
	var limit int

	cmd := &cobra.Command{
		Use:     "log [conversation-id]",
		Aliases: []string{"logs"},
		Short:   "Display the full turn-by-turn conversation log with messages and tool calls",
		Long: `Print the turn-by-turn transcript log of a conversation.
By default, tool calls are suppressed unless --tools is passed.
Use --thinking to display internal model thinking/reasoning blocks where available locally.
Use --no-errors (or --no.errors) to hide harness errors (rate limits, timeouts, out of tokens).

Examples:
  ai log 046f0687
  ai log 046f0687 --tools
  ai log 046f0687 --thinking
  ai log --last --no-errors`,
		RunE: func(cmd *cobra.Command, args []string) error {
			provName := targetProvider
			if provName == "" {
				provName = o.provider
			}

			var convoID string
			if len(args) > 0 {
				convoID = args[0]
			} else if o.last {
				// Pick most recent conversation
				var mostRecent provider.Provider
				var latestTime int64
				var targetCID string
				candidates := provider.All()
				if provName != "" {
					p, err := provider.Get(provName)
					if err != nil {
						return err
					}
					candidates = []provider.Provider{p}
				}
				for _, prov := range candidates {
					convos, err := prov.ListConversations(provider.HistoryOptions{Last: true})
					if err == nil && len(convos) > 0 {
						if convos[0].UpdatedAt.Unix() > latestTime {
							latestTime = convos[0].UpdatedAt.Unix()
							mostRecent = prov
							targetCID = convos[0].ID
						}
					}
				}
				if mostRecent == nil {
					return fmt.Errorf("no conversations found")
				}
				convoID = targetCID
				provName = mostRecent.Name()
			} else {
				return fmt.Errorf("conversation ID is required (or specify --last)")
			}

			var detail *provider.ConversationDetail
			if provName != "" {
				p, err := provider.Get(provName)
				if err != nil {
					return err
				}
				d, err := p.GetConversation(convoID)
				if err == nil && d != nil {
					detail = d
				}
			} else {
				for _, p := range provider.All() {
					d, err := p.GetConversation(convoID)
					if err == nil && d != nil {
						detail = d
						break
					}
				}
			}

			if detail == nil {
				return fmt.Errorf("conversation not found: %s", convoID)
			}
			o.timer.Step("conversation_fetched")

			// Filter turns if tool calls or harness errors are disabled
			var filteredTurns []provider.TurnInfo
			for _, t := range detail.Turns {
				if noErrors && t.IsHarnessError {
					continue
				}
				if !showTools && t.Role == "system" && t.ToolCall != "" && strings.TrimSpace(t.Content) == "" {
					continue
				}
				// Skip empty turns if they have no visible content and thinking is not enabled or empty
				if strings.TrimSpace(t.Content) == "" && (!showThinking || strings.TrimSpace(t.Thinking) == "") {
					continue
				}
				filteredTurns = append(filteredTurns, t)
			}

			if limit > 0 && len(filteredTurns) > limit {
				filteredTurns = filteredTurns[len(filteredTurns)-limit:]
			}

			if o.isJSON() {
				return o.printJSON(cmd.OutOrStdout(), map[string]any{
					"conversation_id": detail.Summary.ID,
					"provider":        detail.Summary.Provider,
					"title":           detail.Summary.Title,
					"turns_count":     len(filteredTurns),
					"turns":           filteredTurns,
				})
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, bold.Sprintf("=== CONVERSATION LOG: %s [%s] ===", detail.Summary.Title, strings.ToUpper(detail.Summary.Provider)))
			fmt.Fprintf(out, "Conversation ID : %s\n", detail.Summary.ID)
			fmt.Fprintf(out, "Total Turns     : %d\n", len(filteredTurns))
			fmt.Fprintln(out, strings.Repeat("─", 80))

			for _, turn := range filteredTurns {
				roleTag := cyan.Sprint(strings.ToUpper(turn.Role))
				if turn.IsHarnessError {
					roleTag = red.Sprint("HARNESS ERROR")
				} else if turn.Role == "user" {
					roleTag = green.Sprint("USER")
				} else if turn.Role == "assistant" {
					roleTag = bold.Sprint("ASSISTANT")
				} else if turn.Role == "" {
					roleTag = cyan.Sprint("NOTE")
				}

				header := fmt.Sprintf("[%s] %s", roleTag, turn.Timestamp.Format("15:04:05"))
				if turn.ToolCall != "" {
					header += fmt.Sprintf(" (tool: %s)", yellow.Sprint(turn.ToolCall))
				}
				fmt.Fprintf(out, "\n%s\n", header)

				if showThinking && turn.Thinking != "" {
					fmt.Fprintln(out, cyan.Sprintf("╭─ Thought / Reasoning ────────────────────────────────────"))
					for _, line := range strings.Split(turn.Thinking, "\n") {
						fmt.Fprintf(out, "%s %s\n", cyan.Sprint("│"), line)
					}
					fmt.Fprintln(out, cyan.Sprintf("╰──────────────────────────────────────────────────────────"))
				}

				if turn.Content != "" {
					if turn.IsHarnessError {
						fmt.Fprintln(out, red.Sprint(turn.Content))
					} else {
						fmt.Fprintln(out, turn.Content)
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&targetProvider, "provider", "p", "", "Target provider")
	cmd.Flags().BoolVar(&showTools, "tools", false, "Include tool executions and calls in output")
	cmd.Flags().BoolVar(&showThinking, "thinking", false, "Include thinking/reasoning blocks in output (where available locally)")
	cmd.Flags().BoolVar(&noErrors, "no-errors", false, "Hide harness-level errors (rate limits, timeouts, token exhaustion)")
	cmd.Flags().BoolVar(&noErrors, "no.errors", false, "Hide harness-level errors (alias for --no-errors)")
	_ = cmd.Flags().MarkHidden("no.errors")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "Maximum number of recent turns to display (0 for all)")

	return cmd
}
