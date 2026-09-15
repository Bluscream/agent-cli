package cli

import (
	"fmt"
	"math"
	"strings"
	"time"

	"agentcli.local/ai/internal/provider"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

func limitsCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:     "limits",
		Aliases: []string{"limit", "quota"},
		Short:   "Show account usage limits and quota consumption across all providers",
		Long: `Displays quota, rate-limit, and usage information for each installed AI
provider. Data is read directly from local provider files — no network call
is made. Where no local data is available a note is shown instead.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLimits(o, cmd)
		},
	}
}

func runLimits(o *options, cmd *cobra.Command) error {
	providers := provider.All()

	var all []provider.LimitInfo
	var errs []string
	for _, p := range providers {
		if o.provider != "" && p.Name() != o.provider {
			continue
		}
		lims, err := p.GetLimits()
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p.Name(), err))
			continue
		}
		all = append(all, lims...)
	}

	if o.isJSON() {
		return o.printJSON(cmd.OutOrStdout(), all)
	}

	out := cmd.OutOrStdout()

	if len(all) == 0 {
		fmt.Fprintln(out, "No limit data found. Are any providers installed?")
		for _, e := range errs {
			fmt.Fprintln(out, "  [!]", e)
		}
		return nil
	}

	// Separate rows that have actual numeric data from pure-informational ones.
	// Only data rows appear in the table; info rows are printed as compact
	// footnotes below so the table stays actionable and uncluttered.
	var dataRows []provider.LimitInfo
	var infoRows []provider.LimitInfo
	for _, l := range all {
		hasData := l.Used >= 0 || l.UsedPct >= 0 || l.Limit >= 0
		if hasData {
			dataRows = append(dataRows, l)
		} else {
			infoRows = append(infoRows, l)
		}
	}

	t := o.newTable(out)
	t.AppendHeader(table.Row{"Provider", "Limit / Quota", "Used", "Bar", "Refill In", "Refill Cycle"})
	// Fixed: provider(~12) + name(~28) + used(~16) + bar(~10) + refill-in(~10) + cycle(~20) + chrome = ~106

	now := time.Now()
	for _, l := range dataRows {
		usedCell := "-"
		if l.Used >= 0 {
			if l.Limit > 0 {
				usedCell = fmt.Sprintf("%s / %s %s", fmtCount(l.Used), fmtCount(l.Limit), l.Unit)
			} else {
				usedCell = fmt.Sprintf("%s %s", fmtCount(l.Used), l.Unit)
			}
		} else if l.UsedPct >= 0 {
			usedCell = fmt.Sprintf("%.0f%% used", l.UsedPct)
		} else if l.Limit >= 0 {
			// Balance-style: only available balance is known (e.g. credits)
			usedCell = fmt.Sprintf("%s %s available", fmtCount(l.Limit), l.Unit)
		}

		barCell := limitBar(l.UsedPct)

		refillInCell := "-"
		if !l.RefillAt.IsZero() {
			d := l.RefillAt.Sub(now)
			if d < 0 {
				refillInCell = faint("now")
			} else {
				refillInCell = fmtDuration(d)
			}
		}

		cycleCell := l.RefillEvery
		if cycleCell == "" || cycleCell == "N/A" {
			cycleCell = "-"
		}

		t.AppendRow(table.Row{
			l.Provider,
			l.Name,
			usedCell,
			barCell,
			refillInCell,
			cycleCell,
		})
	}

	if len(dataRows) > 0 {
		fmt.Fprintln(out, o.renderTable(t))
	} else {
		fmt.Fprintln(out, "No quantitative limit data found locally.")
	}

	// Compact informational footnotes (plan names, auth mode, web links, etc.)
	if len(infoRows) > 0 {
		for _, l := range infoRows {
			if l.Note != "" {
				fmt.Fprintf(out, "  %s  %s: %s\n", faint(l.Provider), l.Name, l.Note)
			}
		}
	}

	for _, e := range errs {
		fmt.Fprintf(out, "  [!] %s\n", e)
	}
	return nil
}

// limitBar renders an 8-char wide ASCII bar for the given percentage (0–100).
// pct < 0 means unknown → shows "????????".
// Colour: green ≤50%, yellow ≤80%, red >80%.
func limitBar(pct float64) string {
	const width = 8
	if pct < 0 {
		return faint("-")
	}
	filled := int(math.Round(pct / 100.0 * float64(width)))
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	var c text.Colors
	switch {
	case pct > 80:
		c = red
	case pct > 50:
		c = yellow
	default:
		c = green
	}
	return c.Sprint(bar)
}

func fmtCount(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
}

func fmtDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}
