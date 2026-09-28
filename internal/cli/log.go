package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"agentcli.local/ai/internal/ingest"
	"agentcli.local/ai/internal/provider"
	"agentcli.local/ai/internal/search"
	"github.com/spf13/cobra"
)

// grepTurns keeps the turns matching m, each surrounded by contextTurns
// neighbours, and reports how many turns matched on their own.
func grepTurns(turns []provider.TurnInfo, m *search.Matcher, contextTurns int) ([]provider.TurnInfo, int) {
	keep := make([]bool, len(turns))
	matches := 0

	for i, t := range turns {
		matched, _ := m.FindMatch(t.Content, 0)
		if !matched && t.Thinking != "" {
			matched, _ = m.FindMatch(t.Thinking, 0)
		}
		if !matched && t.ToolCall != "" {
			matched, _ = m.FindMatch(t.ToolCall, 0)
		}
		if !matched {
			continue
		}
		matches++

		low := max(i-contextTurns, 0)
		high := min(i+contextTurns, len(turns)-1)
		for j := low; j <= high; j++ {
			keep[j] = true
		}
	}

	var kept []provider.TurnInfo
	for i, ok := range keep {
		if ok {
			kept = append(kept, turns[i])
		}
	}
	return kept, matches
}

func logCommand(o *options) *cobra.Command {
	var targetProvider string
	var showTools bool
	var showThinking bool
	var showSystem bool
	var noErrors bool
	var limit int
	var grepText string
	var grepPattern string
	var contextTurns int
	var remote bool

	cmd := &cobra.Command{
		Use:     "log [conversation-id]",
		Aliases: []string{"logs"},
		Short:   "Display the full turn-by-turn conversation log with messages and tool calls",
		Long: `Print the turn-by-turn transcript log of a conversation.
By default, tool calls and system instructions/prompts are suppressed unless --tools or --system is passed.
Use --thinking to display internal model thinking/reasoning blocks where available locally.
Use --system to display system instructions, developer prompts, and system context turns.
Use --no-errors (or --no.errors) to hide harness errors (rate limits, timeouts, out of tokens).
Use --grep (or --grep-pattern for a regex) to show only the turns that mention something,
and --context to include neighbouring turns around each match.

Examples:
  ai log 046f0687
  ai log 046f0687 --tools
  ai log 046f0687 --system
  ai log 046f0687 --thinking
  ai log 046f0687 --grep "deploy" --context 2
  ai log 046f0687 --grep-pattern "git\\s+(commit|push)"
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
				latest, err := provider.MostRecent(provName)
				if err != nil {
					return err
				}
				convoID = latest.Summary.ID
				provName = latest.Provider.Name()
			} else {
				return fmt.Errorf("conversation ID is required (or specify --last)")
			}

			var detail *provider.ConversationDetail
			if remote {
				cfg, err := remoteConfig()
				if err != nil {
					return err
				}
				detail, err = ingest.RemoteConversation(cmd.Context(), cfg, convoID)
				if err != nil {
					return err
				}
			} else {
				// Locate errors on an id matching several providers instead of
				// printing whichever registered first.
				located, err := provider.Locate(provName, convoID)
				if err != nil {
					return err
				}
				detail, err = located.Provider.GetConversation(located.Summary.ID)
				if err != nil {
					return err
				}
			}

			if detail == nil {
				return fmt.Errorf("conversation not found: %s", convoID)
			}
			o.timer.Step("conversation_fetched")

			// Filter turns if tool calls, harness errors, or system turns are disabled
			var filteredTurns []provider.TurnInfo
			for _, t := range detail.Turns {
				if noErrors && t.IsHarnessError {
					continue
				}
				if !showSystem && t.Role == "system" && t.ToolCall == "" && !t.IsHarnessError {
					continue
				}
				if !showTools && (t.ToolCall != "" || t.Role == "tool") {
					continue
				}
				// Skip empty turns if they have no visible content and thinking is not enabled or empty
				if strings.TrimSpace(t.Content) == "" && (!showThinking || strings.TrimSpace(t.Thinking) == "") && !(showTools && t.ToolCall != "") {
					continue
				}
				filteredTurns = append(filteredTurns, t)
			}

			matchedTurns := -1
			if grepText != "" || grepPattern != "" {
				matcher, err := search.NewMatcher(grepText, grepPattern, false, false)
				if err != nil {
					return err
				}
				filteredTurns, matchedTurns = grepTurns(filteredTurns, matcher, contextTurns)
			}

			if limit > 0 && len(filteredTurns) > limit {
				filteredTurns = filteredTurns[len(filteredTurns)-limit:]
			}

			if o.isJSON() {
				payload := map[string]any{
					"conversation_id": detail.Summary.ID,
					"provider":        detail.Summary.Provider,
					"title":           detail.Summary.Title,
					"turns_count":     len(filteredTurns),
					"turns":           filteredTurns,
				}
				if matchedTurns >= 0 {
					payload["matched_turns"] = matchedTurns
				}
				return o.printJSON(cmd.OutOrStdout(), payload)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, bold.Sprintf("=== CONVERSATION LOG: %s [%s] ===", detail.Summary.Title, strings.ToUpper(detail.Summary.Provider)))
			fmt.Fprintf(out, "Conversation ID : %s\n", detail.Summary.ID)
			fmt.Fprintf(out, "Total Turns     : %d\n", len(filteredTurns))
			if matchedTurns >= 0 {
				fmt.Fprintf(out, "Matching Turns  : %d\n", matchedTurns)
			}
			fmt.Fprintln(out, strings.Repeat("─", 80))

			for _, turn := range filteredTurns {
				roleTag := cyan.Sprint(strings.ToUpper(turn.Role))
				if turn.IsHarnessError {
					roleTag = red.Sprint("HARNESS ERROR")
				} else if turn.Role == "system" {
					roleTag = yellow.Sprint("SYSTEM")
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

				if len(turn.ToolArguments) > 0 {
					var arguments bytes.Buffer
					if err := json.Indent(&arguments, turn.ToolArguments, "", "  "); err != nil {
						return fmt.Errorf("format tool arguments: %w", err)
					}
					fmt.Fprintf(out, "Arguments:\n%s\n", arguments.String())
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
	cmd.Flags().BoolVar(&showSystem, "system", false, "Include system instructions, developer prompts, and system context")
	cmd.Flags().BoolVar(&noErrors, "no-errors", false, "Hide harness-level errors (rate limits, timeouts, token exhaustion)")
	cmd.Flags().BoolVar(&noErrors, "no.errors", false, "Hide harness-level errors (alias for --no-errors)")
	_ = cmd.Flags().MarkHidden("no.errors")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "Maximum number of recent turns to display (0 for all)")
	cmd.Flags().StringVar(&grepText, "grep", "", "Show only turns containing this text (case-insensitive)")
	cmd.Flags().StringVar(&grepPattern, "grep-pattern", "", "Show only turns matching this regular expression")
	cmd.Flags().IntVarP(&contextTurns, "context", "C", 0, "Turns of context to show around each --grep match")
	addRemoteFlag(cmd, &remote)

	return cmd
}
