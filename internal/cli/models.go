package cli

import (
	"fmt"
	"strings"

	"agentcli.local/ai/internal/provider"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

func modelsCommand(o *options) *cobra.Command {
	var showDescriptions bool
	cmd := &cobra.Command{
		Use:     "models",
		Aliases: []string{"model"},
		Short:   "List available AI models across all providers",
		Long: `Displays all models available from each installed AI provider.
Data is read dynamically from local provider configurations and session caches.

The active / currently-selected model is highlighted with a ★ marker.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runModels(o, cmd, showDescriptions)
		},
	}

	cmd.Flags().BoolVarP(&showDescriptions, "descriptions", "d", false, "Include model descriptions in tabular output")
	return cmd
}

func runModels(o *options, cmd *cobra.Command, showDescriptions bool) error {
	providers := provider.All()
	if o.provider != "" {
		p, err := provider.Get(o.provider)
		if err != nil {
			return err
		}
		providers = []provider.Provider{p}
	}

	var all []provider.ModelInfo
	var errs []string
	for _, p := range providers {
		mods, err := p.GetModels()
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p.Name(), err))
			continue
		}
		all = append(all, mods...)
	}

	if o.isJSON() {
		return o.printJSON(cmd.OutOrStdout(), all)
	}

	out := cmd.OutOrStdout()

	if len(all) == 0 {
		fmt.Fprintln(out, "No model data found. Are any providers installed?")
		for _, e := range errs {
			fmt.Fprintln(out, "  [!]", e)
		}
		return nil
	}

	t := o.newTable(out)
	if showDescriptions {
		t.AppendHeader(table.Row{"Provider", "Model ID", "Display Name", "Context", "Modalities", "Tiers", "Reasoning", "Description"})
		t.SetColumnConfigs([]table.ColumnConfig{o.flexColConfig(out, 8, 118)})
	} else {
		t.AppendHeader(table.Row{"Provider", "Model ID", "Display Name", "Context", "Modalities", "Tiers", "Reasoning"})
		t.SetColumnConfigs([]table.ColumnConfig{o.flexColConfig(out, 3, 90)})
	}

	for _, m := range all {
		idCell := "-"
		displayCell := m.DisplayName

		if m.ID != "" && m.ID != m.DisplayName {
			idCell = m.ID
			if m.IsActive {
				idCell = bold.Sprint("★ " + m.ID)
			}
		} else {
			if m.IsActive {
				displayCell = bold.Sprint("★ " + m.DisplayName)
			}
		}

		ctxCell := "-"
		if m.ContextWindow > 0 {
			if m.ContextWindow >= 1_000_000 {
				ctxCell = fmt.Sprintf("%.0fM", float64(m.ContextWindow)/1_000_000)
			} else if m.ContextWindow >= 1000 {
				ctxCell = fmt.Sprintf("%dK", m.ContextWindow/1000)
			} else {
				ctxCell = fmt.Sprintf("%d", m.ContextWindow)
			}
		}

		modalCell := joinOrDash(m.InputModalities)
		tiersCell := joinOrDash(m.ServiceTiers)
		reasonCell := joinOrDash(m.ReasoningLevels)

		if showDescriptions {
			t.AppendRow(table.Row{
				m.Provider,
				idCell,
				displayCell,
				ctxCell,
				modalCell,
				tiersCell,
				reasonCell,
				m.Description,
			})
		} else {
			t.AppendRow(table.Row{
				m.Provider,
				idCell,
				displayCell,
				ctxCell,
				modalCell,
				tiersCell,
				reasonCell,
			})
		}
	}

	fmt.Fprintln(out, o.renderTable(t))

	for _, e := range errs {
		fmt.Fprintf(out, "  [!] %s\n", e)
	}
	return nil
}

func joinOrDash(ss []string) string {
	if len(ss) == 0 {
		return "-"
	}
	return strings.Join(ss, ", ")
}
