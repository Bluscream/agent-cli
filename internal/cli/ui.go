package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"golang.org/x/term"
)

func (o *options) applyColor(out io.Writer) {
	switch o.color {
	case "always":
		text.EnableColors()
		return
	case "never":
		text.DisableColors()
		return
	}
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		text.DisableColors()
		return
	}
	f, isFile := out.(*os.File)
	if isFile && term.IsTerminal(int(f.Fd())) {
		text.EnableColors()
		return
	}
	text.DisableColors()
}

// termWidth returns the current terminal column count.
// It checks $COLUMNS first, then queries the tty, and returns 0 when the
// output is piped / non-interactive (callers treat 0 as "no limit").
func termWidth(w io.Writer) int {
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	if f, ok := w.(*os.File); ok {
		if c, _, err := term.GetSize(int(f.Fd())); err == nil && c > 0 {
			return c
		}
	}
	return 0
}

func (o *options) newTable(w io.Writer) table.Writer {
	if o.format == "csv" {
		text.DisableColors()
	} else {
		o.applyColor(w)
	}
	t := table.NewWriter()

	s := table.StyleRounded
	s.Options.SeparateRows = false
	s.Format.Header = text.FormatUpper
	s.Color.Header = text.Colors{text.Bold}
	t.SetStyle(s)

	// Constrain the table to the terminal width so it never overflows.
	// SetAllowedRowLength clips the rendered row to exactly that many
	// rune-columns, appending "~" on any line that got truncated.
	if cols := termWidth(w); cols > 0 {
		t.SetAllowedRowLength(cols)
	}

	return t
}

// flexColConfig returns a ColumnConfig for a "flexible" (wrappable) column —
// one whose content (titles, paths, descriptions) should word-wrap rather
// than cause the table to overflow.
//
//	colNum    — 1-based column number
//	fixedCost — total character width consumed by all fixed columns + table
//	            chrome (borders, padding, separators).  The flexible column
//	            gets whatever is left, floored at minWidth.
func (o *options) flexColConfig(w io.Writer, colNum, fixedCost int) table.ColumnConfig {
	const minWidth = 20
	cols := termWidth(w)
	available := 80
	if cols > 0 {
		available = cols - fixedCost
		if available < minWidth {
			available = minWidth
		}
	}
	if o.maxColLength >= 0 && available > o.maxColLength {
		available = o.maxColLength
	}
	return table.ColumnConfig{Number: colNum, WidthMax: available}
}

func (o *options) newDetail(w io.Writer) table.Writer {
	t := o.newTable(w)
	t.Style().Options.SeparateHeader = false
	// Column 1 (key label) is narrow; column 2 (value) gets the rest.
	// fixedCost: 2 border chars + padding + ~16 chars for the widest key label.
	cfg := []table.ColumnConfig{
		{Number: 1, Colors: text.Colors{text.FgCyan}},
		o.flexColConfig(w, 2, 22),
	}
	t.SetColumnConfigs(cfg)
	return t
}

func (o *options) renderTable(t table.Writer) string {
	if !o.withHeader {
		t.ResetHeaders()
	}
	if o.maxColLength >= 0 {
		// Apply WidthMax: o.maxColLength across all columns
		var cfgs []table.ColumnConfig
		for col := 1; col <= 30; col++ {
			cfgs = append(cfgs, table.ColumnConfig{
				Number:   col,
				WidthMax: o.maxColLength,
			})
		}
		t.SetColumnConfigs(cfgs)
	}
	if o.format == "csv" {
		return t.RenderCSV()
	}
	return t.Render()
}

func (o *options) printJSON(w io.Writer, val any) error {
	payload := val
	if o.timer != nil && o.timer.IsEnabled() {
		payload = map[string]any{
			"data":   val,
			"_debug": o.timer.Metrics(),
		}
	}
	var data []byte
	var err error
	if o.format == "compact" {
		data, err = json.Marshal(payload)
	} else {
		data, err = json.MarshalIndent(payload, "", "  ")
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

func (o *options) isJSON() bool {
	return o.format == "json" || o.format == "compact"
}

func detailRows(t table.Writer, pairs ...[2]string) {
	for _, p := range pairs {
		if strings.TrimSpace(p[1]) != "" {
			t.AppendRow(table.Row{p[0], p[1]})
		}
	}
}

func kv(k, v string) [2]string { return [2]string{k, v} }

var (
	green  = text.Colors{text.FgGreen}
	red    = text.Colors{text.FgRed}
	yellow = text.Colors{text.FgYellow}
	dim    = text.Colors{text.Faint}
	cyan   = text.Colors{text.FgCyan}
	bold   = text.Colors{text.Bold}
)

func colorStatus(s string) string {
	switch strings.ToLower(s) {
	case "normal", "online", "ok", "running", "installed", "enabled", "clean":
		return green.Sprint(strings.ToUpper(s))
	case "slow", "idle", "busy", "working", "installing", "delayed":
		return yellow.Sprint(strings.ToUpper(s))
	case "down", "unreachable", "offline", "error", "failed", "suspended", "disabled", "not installed", "dirty":
		return red.Sprint(strings.ToUpper(s))
	}
	return strings.ToUpper(s)
}

func colorBool(b bool) string {
	if b {
		return green.Sprint("yes")
	}
	return red.Sprint("no")
}

func faint(s string) string { return dim.Sprint(s) }

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func (o *options) sizeCell(n int64) string {
	if o.format == "csv" {
		return strconv.FormatInt(n, 10)
	}
	return humanBytes(n)
}
