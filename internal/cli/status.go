package cli

import (
	"fmt"
	"strings"

	"agentcli.local/ai/internal/provider"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

type ProviderStatusOutput struct {
	provider.ProviderInfo
	Account     *provider.AccountInfo `json:"account,omitempty"`
	ActiveModel string                `json:"active_model,omitempty"`
	Quota       string                `json:"quota,omitempty"`
}

func statusCommand(o *options) *cobra.Command {
	var targetProvider string
	cmd := &cobra.Command{
		Use:     "status",
		Aliases: []string{"info", "agent", "agents", "provider", "providers"},
		Short:   "Display status, logged-in account, limits bar, active model, and resource usage for all installed agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			var provList []provider.Provider
			if targetProvider != "" || o.provider != "" {
				name := targetProvider
				if name == "" {
					name = o.provider
				}
				p, err := provider.Get(name)
				if err != nil {
					return err
				}
				provList = []provider.Provider{p}
			} else {
				provList = provider.All()
			}
			o.timer.Step("providers_resolved")

			var items []ProviderStatusOutput
			for _, p := range provList {
				st, err := p.Status()
				if err != nil {
					continue
				}

				acc, _ := p.GetActiveAccount()
				limits, _ := p.GetLimits()
				models, _ := p.GetModels()

				activeModel := ""
				for _, m := range models {
					if m.IsActive {
						activeModel = m.DisplayName
						if activeModel == "" {
							activeModel = m.ID
						}
						break
					}
				}

				quotaSummary := summarizeLimits(limits)

				items = append(items, ProviderStatusOutput{
					ProviderInfo: st,
					Account:      acc,
					ActiveModel:  activeModel,
					Quota:        quotaSummary,
				})
			}
			o.timer.Step("provider_statuses_queried")

			if o.isJSON() {
				return o.printJSON(cmd.OutOrStdout(), items)
			}

			out := cmd.OutOrStdout()
			t := o.newTable(out)
			t.AppendHeader(table.Row{"Provider", "Account", "State", "Active Model", "Limits / Quota", "MCPs", "CPU", "RAM"})
			// Fixed cols: provider(~14) + state(~10) + active_model(~22) + quota(~20) + mcps(~6) + cpu(~8) + ram(~8) = ~88
			t.SetColumnConfigs([]table.ColumnConfig{o.flexColConfig(out, 2, 88)})

			for _, it := range items {
				s := it.ProviderInfo
				stateStr := "IDLE"
				if !s.Installed {
					stateStr = "NOT INSTALLED"
				} else if !s.Running {
					stateStr = "STOPPED"
				} else if s.Busy {
					stateStr = "BUSY"
				}

				accountCell := "-"
				if it.Account != nil {
					if it.Account.Email != "" && it.Account.Email != "-" {
						accountCell = it.Account.Email
					} else if it.Account.DisplayName != "" {
						accountCell = it.Account.DisplayName
					} else if it.Account.ID != "" {
						accountCell = it.Account.ID
					}
				}

				modelCell := "-"
				if it.ActiveModel != "" {
					modelCell = "★ " + it.ActiveModel
				}

				quotaCell := it.Quota
				if quotaCell == "" {
					quotaCell = "-"
				}

				cpuStr := "-"
				ramStr := "-"
				if s.Running {
					cpuStr = fmt.Sprintf("%.1f%%", s.CPUPercent)
					ramStr = humanBytes(s.MemoryRSSBytes)
				}

				t.AppendRow(table.Row{
					s.DisplayName,
					accountCell,
					colorStatus(stateStr),
					modelCell,
					quotaCell,
					s.MCPCount,
					cpuStr,
					ramStr,
				})
			}

			fmt.Fprintln(out, o.renderTable(t))
			return nil
		},
	}

	cmd.Flags().StringVarP(&targetProvider, "provider", "p", "", "Filter status by agent provider (antigravity, claude, codex)")
	return cmd
}

// summarizeLimits produces a concise 1-line progress bar or credit summary for a provider.
func summarizeLimits(limits []provider.LimitInfo) string {
	var primaryPct *float64
	var pctLabel string
	var credits int64 = -1

	for _, l := range limits {
		if l.UsedPct >= 0 && primaryPct == nil {
			val := l.UsedPct
			primaryPct = &val
			if strings.Contains(strings.ToLower(l.Name), "fast") {
				pctLabel = "5h"
			} else if strings.Contains(strings.ToLower(l.Name), "daily") {
				pctLabel = "daily"
			}
		}
		if l.Unit == "credits" && l.Limit >= 0 {
			credits = l.Limit
		}
	}

	if primaryPct != nil {
		bar := limitBar(*primaryPct)
		if pctLabel != "" {
			return fmt.Sprintf("%s %.0f%% (%s)", bar, *primaryPct, pctLabel)
		}
		return fmt.Sprintf("%s %.0f%%", bar, *primaryPct)
	}

	if credits >= 0 {
		return fmt.Sprintf("%d credits", credits)
	}

	for _, l := range limits {
		if l.Used >= 0 && l.Unit == "tokens" {
			return fmt.Sprintf("%s tok", fmtCount(l.Used))
		}
	}

	return "-"
}
