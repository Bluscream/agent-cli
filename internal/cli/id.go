package cli

import (
	"fmt"
	"strings"

	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/mcp"
	"agentcli.local/ai/internal/provider"
	"github.com/spf13/cobra"
)

func idCommand(o *options) *cobra.Command {
	var targetProvider string

	cmd := &cobra.Command{
		Use:   "id <identifier>",
		Short: "Resolve any identifier (Short ID, UUID, memory, skill, or MCP server) across all providers",
		Long: `Generic lookup tool that resolves any short ID or full raw ID to the underlying entity
(conversation, memory/knowledge item, skill, or MCP server) and prints detailed metadata.

Examples:
  ai id 046f0687
  ai id ba07b876
  ai id omni-mcp
  ai id 98256169-57f7-4239-9eaf-c73532e3253d`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("identifier argument is required: ai id <identifier>")
			}
			query := strings.TrimSpace(args[0])

			var provList []provider.Provider
			if targetProvider != "" {
				p, err := provider.Get(targetProvider)
				if err != nil {
					return err
				}
				provList = []provider.Provider{p}
			} else if o.provider != "" {
				p, err := provider.Get(o.provider)
				if err != nil {
					return err
				}
				provList = []provider.Provider{p}
			} else {
				provList = provider.All()
			}

			// 1. Check Conversations
			for _, p := range provList {
				detail, err := p.GetConversation(query)
				if err == nil && detail != nil {
					if o.isJSON() {
						return o.printJSON(cmd.OutOrStdout(), map[string]any{
							"type":         "conversation",
							"conversation": detail,
						})
					}

					out := cmd.OutOrStdout()
					fmt.Fprintf(out, "%s %s [%s]\n\n",
						bold.Sprint("RESOLVED TYPE: Conversation"),
						cyan.Sprint(idutil.ShortID(detail.Summary.ID)),
						strings.ToUpper(detail.Summary.Provider),
					)

					dt := o.newDetail(out)
					detailRows(dt,
						kv("Entity Type", "Conversation"),
						kv("Provider", detail.Summary.Provider),
						kv("Short ID", idutil.ShortID(detail.Summary.ID)),
						kv("Full Raw ID", detail.Summary.ID),
						kv("Title", detail.Summary.Title),
						kv("Created", detail.Summary.CreatedAt.Format("2006-01-02 15:04:05")),
						kv("Last Modified", detail.Summary.UpdatedAt.Format("2006-01-02 15:04:05")),
						kv("Workspace", detail.Summary.WorkspaceDir),
						kv("Transcript", detail.TranscriptPath),
					)
					fmt.Fprintln(out, o.renderTable(dt))

					if detail.InitialPrompt != "" {
						fmt.Fprintf(out, "\n%s\n%s\n", bold.Sprint("INITIAL USER PROMPT:"), strings.TrimSpace(detail.InitialPrompt))
					}
					if detail.LastResponse != "" {
						fmt.Fprintf(out, "\n%s\n%s\n", bold.Sprint("LAST AGENT RESPONSE:"), strings.TrimSpace(detail.LastResponse))
					}
					return nil
				}
			}

			// 2. Check Memories
			for _, p := range provList {
				mems, err := p.ListMemories()
				if err == nil {
					for _, m := range mems {
						if idutil.Match(query, m.ID) {
							if o.isJSON() {
								return o.printJSON(cmd.OutOrStdout(), map[string]any{
									"type":   "memory",
									"memory": m,
								})
							}

							out := cmd.OutOrStdout()
							fmt.Fprintf(out, "%s %s [%s]\n\n",
								bold.Sprint("RESOLVED TYPE: Memory / Knowledge Item"),
								cyan.Sprint(idutil.ShortID(m.ID)),
								strings.ToUpper(m.Provider),
							)

							dt := o.newDetail(out)
							detailRows(dt,
								kv("Entity Type", "Memory / Knowledge Item"),
								kv("Provider", m.Provider),
								kv("Short ID", idutil.ShortID(m.ID)),
								kv("Full Raw ID", m.ID),
								kv("Title", m.Title),
								kv("Source File", m.SourceFile),
								kv("Created", m.CreatedAt.Format("2006-01-02 15:04:05")),
								kv("Last Updated", m.UpdatedAt.Format("2006-01-02 15:04:05")),
							)
							fmt.Fprintln(out, o.renderTable(dt))
							fmt.Fprintf(out, "\n%s\n\n%s\n", bold.Sprint("CONTENT:"), strings.TrimSpace(m.Content))
							return nil
						}
					}
				}
			}

			// 3. Check Skills
			for _, p := range provList {
				skills, err := p.ListSkills()
				if err == nil {
					for _, s := range skills {
						if idutil.Match(query, s.Name) || idutil.Match(query, s.Path) {
							if o.isJSON() {
								return o.printJSON(cmd.OutOrStdout(), map[string]any{
									"type":  "skill",
									"skill": s,
								})
							}

							out := cmd.OutOrStdout()
							fmt.Fprintf(out, "%s %s [%s]\n\n",
								bold.Sprint("RESOLVED TYPE: Agent Skill"),
								cyan.Sprint(s.Name),
								strings.ToUpper(s.Provider),
							)

							typeStr := "Custom"
							if s.IsBuiltin {
								typeStr = "Built-in"
							}

							dt := o.newDetail(out)
							detailRows(dt,
								kv("Entity Type", "Agent Skill"),
								kv("Provider", s.Provider),
								kv("Skill Name", s.Name),
								kv("Type", typeStr),
								kv("Description", s.Description),
								kv("File Path", s.Path),
								kv("Rules Count", fmt.Sprintf("%d", s.RulesCount)),
								kv("Scripts Count", fmt.Sprintf("%d", s.ScriptsCount)),
							)
							fmt.Fprintln(out, o.renderTable(dt))
							return nil
						}
					}
				}
			}

			// 4. Check MCP Servers
			mcpMgr := mcp.NewManager()
			mcpServers, err := mcpMgr.List()
			if err == nil {
				for _, s := range mcpServers {
					if idutil.Match(query, s.Name) {
						if o.isJSON() {
							return o.printJSON(cmd.OutOrStdout(), map[string]any{
								"type":       "mcp_server",
								"mcp_server": s,
							})
						}

						out := cmd.OutOrStdout()
						fmt.Fprintf(out, "%s %s\n\n",
							bold.Sprint("RESOLVED TYPE: MCP Server"),
							cyan.Sprint(s.Name),
						)

						target := s.Config.URL
						if target == "" {
							target = s.Config.Command
							if len(s.Config.Args) > 0 {
								target += " " + strings.Join(s.Config.Args, " ")
							}
						}

						dt := o.newDetail(out)
						detailRows(dt,
							kv("Entity Type", "MCP Server"),
							kv("Server Name", s.Name),
							kv("Target", target),
							kv("Status", colorStatus(map[bool]string{true: "disabled", false: "active"}[s.Disabled])),
							kv("Config Files", strings.Join(s.Files, "\n")),
						)
						fmt.Fprintln(out, o.renderTable(dt))
						return nil
					}
				}
			}

			// 5. Check Plugins
			for _, p := range provList {
				plugins, err := p.ListPlugins()
				if err == nil {
					for _, pl := range plugins {
						if idutil.Match(query, pl.Name) || idutil.Match(query, pl.Path) {
							if o.isJSON() {
								return o.printJSON(cmd.OutOrStdout(), map[string]any{
									"type":   "plugin",
									"plugin": pl,
								})
							}

							out := cmd.OutOrStdout()
							fmt.Fprintf(out, "%s %s [%s]\n\n",
								bold.Sprint("RESOLVED TYPE: Agent Plugin"),
								cyan.Sprint(pl.Name),
								strings.ToUpper(pl.Provider),
							)

							dt := o.newDetail(out)
							detailRows(dt,
								kv("Entity Type", "Agent Plugin"),
								kv("Provider", pl.Provider),
								kv("Plugin Name", pl.Name),
								kv("Type", pl.Type),
								kv("Status", colorStatus(pl.Status)),
								kv("Path", pl.Path),
								kv("Description", pl.Description),
							)
							fmt.Fprintln(out, o.renderTable(dt))
							return nil
						}
					}
				}
			}

			// 6. Check Accounts & Saved Profiles
			for _, p := range provList {
				accs, err := p.GetAccounts()
				if err == nil {
					for _, a := range accs {
						if idutil.Match(query, a.ID) || idutil.Match(query, a.DisplayName) || (a.Email != "" && a.Email != "-" && idutil.Match(query, a.Email)) {
							if o.isJSON() {
								return o.printJSON(cmd.OutOrStdout(), map[string]any{
									"type":    "account",
									"account": a,
								})
							}

							out := cmd.OutOrStdout()
							fmt.Fprintf(out, "%s %s [%s]\n\n",
								bold.Sprint("RESOLVED TYPE: Agent Account / Profile"),
								cyan.Sprint(a.DisplayName),
								strings.ToUpper(a.Provider),
							)

							statusStr := "ACTIVE"
							if !a.IsActive {
								statusStr = "AVAILABLE (SWITCHABLE)"
							}

							dt := o.newDetail(out)
							detailRows(dt,
								kv("Entity Type", "Agent Account / Profile"),
								kv("Provider", a.Provider),
								kv("Account Name", a.DisplayName),
								kv("ID", a.ID),
								kv("Email", a.Email),
								kv("Subscription Plan", a.Plan),
								kv("Active In", a.ActiveIn),
								kv("Status", colorStatus(statusStr)),
								kv("Config / Profile Path", a.ConfigPath),
							)
							fmt.Fprintln(out, o.renderTable(dt))
							return nil
						}
					}
				}
			}

			// 7. Check Models
			for _, p := range provList {
				models, err := p.GetModels()
				if err == nil {
					for _, m := range models {
						if idutil.Match(query, m.ID) || idutil.Match(query, m.DisplayName) {
							if o.isJSON() {
								return o.printJSON(cmd.OutOrStdout(), map[string]any{
									"type":  "model",
									"model": m,
								})
							}

							out := cmd.OutOrStdout()
							fmt.Fprintf(out, "%s %s [%s]\n\n",
								bold.Sprint("RESOLVED TYPE: AI Model"),
								cyan.Sprint(m.DisplayName),
								strings.ToUpper(m.Provider),
							)

							ctxStr := "-"
							if m.ContextWindow > 0 {
								ctxStr = fmt.Sprintf("%d tokens (%s)", m.ContextWindow, fmtCount(m.ContextWindow))
							}

							activeStr := "No"
							if m.IsActive {
								activeStr = bold.Sprint("Yes (★ Active)")
							}

							dt := o.newDetail(out)
							detailRows(dt,
								kv("Entity Type", "AI Model"),
								kv("Provider", m.Provider),
								kv("Model ID", m.ID),
								kv("Display Name", m.DisplayName),
								kv("Active Model", activeStr),
								kv("Context Window", ctxStr),
								kv("Modalities", strings.Join(m.InputModalities, ", ")),
								kv("Service Tiers", strings.Join(m.ServiceTiers, ", ")),
								kv("Reasoning Levels", strings.Join(m.ReasoningLevels, ", ")),
								kv("Description", m.Description),
							)
							fmt.Fprintln(out, o.renderTable(dt))
							return nil
						}
					}
				}
			}

			return fmt.Errorf("could not resolve identifier '%s' across conversations, memories, skills, MCP servers, plugins, accounts, or models", query)
		},
	}

	cmd.Flags().StringVarP(&targetProvider, "provider", "p", "", "Filter by provider")

	return cmd
}
