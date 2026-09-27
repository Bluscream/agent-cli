package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"agentcli.local/ai/internal/ingest"
)

func TestFormatCountGroupsThousands(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   int
		want string
	}{
		{0, "0"},
		{7, "7"},
		{999, "999"},
		{1000, "1,000"},
		{6771, "6,771"},
		{208289, "208,289"},
		{-1234, "-1,234"},
	} {
		if got := formatCount(tc.in); got != tc.want {
			t.Errorf("formatCount(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatClock(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{0, "00:00"},
		{45 * time.Second, "00:45"},
		{90 * time.Second, "01:30"},
		{80*time.Minute + 5*time.Second, "1:20:05"},
	} {
		if got := formatClock(tc.in); got != tc.want {
			t.Errorf("formatClock(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// truncateCell counted bytes, so a title with multi-byte characters could be
// cut mid-rune and emit replacement characters.
func TestTruncateCellIsRuneSafe(t *testing.T) {
	t.Parallel()
	const title = "ラーメンとカレーの作り方について教えてください"

	got := truncateCell(title, 10)
	if !utf8.ValidString(got) {
		t.Fatalf("truncation produced invalid UTF-8: %q", got)
	}
	if utf8.RuneCountInString(got) != 10 {
		t.Errorf("truncated to %d runes, want 10: %q", utf8.RuneCountInString(got), got)
	}
	if short := truncateCell("hello", 20); short != "hello" {
		t.Errorf("short title was altered: %q", short)
	}
	if collapsed := truncateCell("a\n  b\tc", 20); collapsed != "a b c" {
		t.Errorf("whitespace not collapsed: %q", collapsed)
	}
}

func TestETAOnlyWhenExtrapolable(t *testing.T) {
	t.Parallel()
	if _, ok := (ingest.ProgressEvent{Done: 0, Total: 10, Elapsed: time.Second}).ETA(); ok {
		t.Error("ETA reported before any work completed")
	}
	if _, ok := (ingest.ProgressEvent{Done: 10, Total: 10, Elapsed: time.Second}).ETA(); ok {
		t.Error("ETA reported after the pass finished")
	}

	eta, ok := (ingest.ProgressEvent{Done: 5, Total: 10, Elapsed: 10 * time.Second}).ETA()
	if !ok {
		t.Fatal("no ETA at the halfway point")
	}
	if eta != 10*time.Second {
		t.Errorf("ETA = %s, want 10s", eta)
	}
}

// A redirected run must not emit carriage returns or bar glyphs, or a log file
// fills with redraw debris.
func TestProgressIsPlainWhenNotInteractive(t *testing.T) {
	t.Parallel()
	out := &bytes.Buffer{}
	progress := newIngestProgress(out, false)

	progress.Handle(ingest.ProgressEvent{Done: 1, Total: 2, Elapsed: time.Second,
		Result: ingest.ConversationResult{Provider: "claude", ID: "abcdef1234", Title: "a title", Points: 5}})
	progress.Handle(ingest.ProgressEvent{Done: 2, Total: 2, Elapsed: 2 * time.Second,
		Result: ingest.ConversationResult{Provider: "claude", ID: "beefbeef00", Skipped: true}})
	progress.Finish()

	got := out.String()
	if strings.ContainsAny(got, "\r") {
		t.Errorf("carriage returns leaked into non-interactive output: %q", got)
	}
	if strings.Contains(got, "█") || strings.Contains(got, "░") {
		t.Errorf("progress bar drawn without a terminal: %q", got)
	}
	if !strings.Contains(got, "abcdef12") {
		t.Errorf("published conversation missing from output: %q", got)
	}
	// Skipped conversations are the common case and must stay quiet.
	if strings.Contains(got, "beefbeef") {
		t.Errorf("skipped conversation was reported: %q", got)
	}
}
