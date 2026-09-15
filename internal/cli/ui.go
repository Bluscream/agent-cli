package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

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

	return t
}

func (o *options) flexCol(colNum, width int) table.ColumnConfig {
	if width < 10 {
		width = 10
	}
	if o.maxColLength >= 0 && width > o.maxColLength {
		width = o.maxColLength
	}
	return table.ColumnConfig{
		Number:           colNum,
		WidthMax:         width,
		WidthMaxEnforcer: text.WrapText,
	}
}

// FlexColSpec defines a flexible column with its column number (1-based),
// minimum width, and relative ratio of available space.
type FlexColSpec struct {
	Number   int
	MinWidth int
	Ratio    int
}

// distributeFlexCols computes ColumnConfigs for a set of flexible columns,
// splitting the available space (terminal width minus fixedCost) according to their ratios.
// If terminal width is unknown or small, it falls back to MinWidth.
func (o *options) distributeFlexCols(w io.Writer, fixedCost int, specs ...FlexColSpec) []table.ColumnConfig {
	if len(specs) == 0 {
		return nil
	}
	totalRatio := 0
	totalMin := 0
	for _, s := range specs {
		r := s.Ratio
		if r <= 0 {
			r = 1
		}
		totalRatio += r
		minW := s.MinWidth
		if minW <= 0 {
			minW = 10
		}
		totalMin += minW
	}

	cols := termWidth(w)
	available := 0
	if cols > 0 {
		available = cols - fixedCost
	}

	configs := make([]table.ColumnConfig, len(specs))
	if available <= totalMin {
		for i, s := range specs {
			minW := s.MinWidth
			if minW <= 0 {
				minW = 10
			}
			configs[i] = o.flexCol(s.Number, minW)
		}
		return configs
	}

	remaining := available
	for i, s := range specs {
		if i == len(specs)-1 {
			configs[i] = o.flexCol(s.Number, remaining)
			break
		}
		r := s.Ratio
		if r <= 0 {
			r = 1
		}
		wCol := (available * r) / totalRatio
		minW := s.MinWidth
		if minW <= 0 {
			minW = 10
		}
		if wCol < minW {
			wCol = minW
		}
		remaining -= wCol
		configs[i] = o.flexCol(s.Number, wCol)
	}

	return configs
}

// flexColConfig returns a ColumnConfig for a single "flexible" (wrappable) column —
// one whose content (titles, paths, descriptions) should word-wrap rather
// than cause the table to overflow.
func (o *options) flexColConfig(w io.Writer, colNum, fixedCost int) table.ColumnConfig {
	const minWidth = 10
	cols := termWidth(w)
	available := 80
	if cols > 0 {
		available = cols - fixedCost
		if available < minWidth {
			available = minWidth
		}
	}
	return o.flexCol(colNum, available)
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

// dateTimeCell formats a timestamp:
// - CSV: standard "2006-01-02 15:04"
// - Today: "15:04" (omits date if from today)
// - Older than today: "2006-01-02 15:04"
func (o *options) dateTimeCell(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	if o.format == "csv" {
		return t.Format("2006-01-02 15:04")
	}
	now := time.Now()
	y1, m1, d1 := t.Local().Date()
	y2, m2, d2 := now.Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		return t.Local().Format("15:04")
	}
	return t.Local().Format("2006-01-02 15:04")
}
