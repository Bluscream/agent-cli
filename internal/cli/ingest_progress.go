package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"agentcli.local/ai/internal/ingest"
	"golang.org/x/term"
)

// ingestProgress draws ingestion progress. On a terminal it redraws a single
// bar in place and prints a line per published conversation above it; when the
// output is redirected it prints plain lines only, so a log file does not fill
// with carriage returns.
type ingestProgress struct {
	out         io.Writer
	interactive bool
	dryRun      bool
	barWidth    int
	lastDrawn   int
}

func newIngestProgress(out io.Writer, dryRun bool) *ingestProgress {
	interactive := false
	if f, ok := out.(*os.File); ok {
		interactive = term.IsTerminal(int(f.Fd()))
	}
	return &ingestProgress{out: out, interactive: interactive, dryRun: dryRun, barWidth: 28}
}

// Handle renders one progress event.
func (p *ingestProgress) Handle(event ingest.ProgressEvent) {
	p.clearLine()

	if line := p.conversationLine(event.Result); line != "" {
		fmt.Fprintln(p.out, line)
	}
	if p.interactive {
		p.drawBar(event)
	}
}

// Finish clears any in-place bar so the summary starts on a clean line.
func (p *ingestProgress) Finish() {
	p.clearLine()
}

func (p *ingestProgress) clearLine() {
	if !p.interactive || p.lastDrawn == 0 {
		return
	}
	fmt.Fprintf(p.out, "\r%s\r", strings.Repeat(" ", p.lastDrawn))
	p.lastDrawn = 0
}

// conversationLine describes one conversation, or "" for a skipped one, which
// is the common case and would otherwise drown out the real work.
func (p *ingestProgress) conversationLine(r ingest.ConversationResult) string {
	if r.Skipped {
		return ""
	}
	if r.Error != "" {
		return fmt.Sprintf("  %s %s %.8s  %s", red.Sprint("failed"), r.Provider, r.ID, r.Error)
	}

	verb := green.Sprint("sent ")
	if p.dryRun {
		verb = yellow.Sprint("would")
	}
	return fmt.Sprintf("  %s %-11s %.8s  %s  %s",
		verb, r.Provider, r.ID, faint(fmt.Sprintf("%6d pts", r.Points)), truncateCell(r.Title, 52))
}

func (p *ingestProgress) drawBar(event ingest.ProgressEvent) {
	if event.Total <= 0 {
		return
	}

	fraction := float64(event.Done) / float64(event.Total)
	filled := int(fraction * float64(p.barWidth))
	bar := strings.Repeat("█", filled) + strings.Repeat("░", p.barWidth-filled)

	eta := "--:--"
	if remaining, ok := event.ETA(); ok {
		eta = formatClock(remaining)
	}

	line := fmt.Sprintf("  %s %3.0f%%  %d/%d  %s pts  %s elapsed  ETA %s",
		bar, fraction*100, event.Done, event.Total,
		formatCount(event.Points), formatClock(event.Elapsed), eta)

	// Measure in runes, not bytes: each bar block is three bytes, so a byte
	// count would both over-erase and wrap the bar onto a second row.
	runes := []rune(line)
	if width := termWidth(p.out); width > 0 && len(runes) > width-1 {
		runes = runes[:width-1]
		line = string(runes)
	}
	fmt.Fprint(p.out, line+"\r")
	p.lastDrawn = len(runes)
}

func formatClock(d time.Duration) string {
	d = d.Round(time.Second)
	if d >= time.Hour {
		return fmt.Sprintf("%d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
	}
	return fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

// formatCount groups thousands so six-figure point totals stay readable.
func formatCount[T int | int64](n T) string {
	digits := fmt.Sprintf("%d", n)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}

	var grouped strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteRune(r)
	}
	return sign + grouped.String()
}

// truncateCell shortens a title to limit characters, counting runes so a
// multi-byte character is never cut in half.
func truncateCell(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	if limit <= 1 {
		return string(runes[:max(limit, 0)])
	}
	return string(runes[:limit-1]) + "…"
}
