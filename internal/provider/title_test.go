package provider

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanTitleStripsMarkdownAndWorkspaceURIs(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"# Hello world\nSome other text", "Hello world"},
		{"* file:///some/path\nSecond line", "Second line"},
		{"  -- fix the audio settings", "fix the audio settings"},
		{"", ""},
	}
	for _, c := range cases {
		if got := CleanTitle(c.input); got != c.want {
			t.Errorf("CleanTitle(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// The escapes arrive as two literal characters from serialised transcripts.
// Claude's copy of this did not unescape them, so its titles carried a \n.
func TestCleanTitleUnescapesSerialisedEscapes(t *testing.T) {
	if got := CleanTitle(`first line\nsecond line`); got != "first line" {
		t.Fatalf("got %q, want %q", got, "first line")
	}
	if got := CleanTitle(`say \"hello\"`); got != `say "hello"` {
		t.Fatalf("got %q", got)
	}
}

// Claude's copy returned nothing for a title made only of markdown
// punctuation; the fallback keeps the first line's text instead.
func TestCleanTitleFallsBackToTheFirstLine(t *testing.T) {
	if got := CleanTitle("###"); got != "" {
		t.Fatalf("got %q, want empty for punctuation only", got)
	}
	// A workspace URI is skipped while looking for a better line, but when it
	// is all there is the fallback keeps it: some title beats none.
	if got := CleanTitle("file:///only/a/path"); got != "file:///only/a/path" {
		t.Fatalf("got %q, want the URI as the fallback title", got)
	}
}

// Both copies truncated on a byte index, which splits a multi-byte character
// and emits U+FFFD.
func TestCleanTitleTruncatesOnRunesNotBytes(t *testing.T) {
	title := CleanTitle(strings.Repeat("é", 200))
	if utf8.RuneCountInString(title) != TitleMaxLength {
		t.Fatalf("title has %d runes, want %d", utf8.RuneCountInString(title), TitleMaxLength)
	}
	if !utf8.ValidString(title) || strings.ContainsRune(title, '�') {
		t.Fatalf("truncation split a character: %q", title)
	}
}

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		in    string
		limit int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 2, "he"},
		{"héllo", 2, "hé"},
		{"hello", 0, "hello"},
		{"", 3, ""},
	}
	for _, c := range cases {
		if got := TruncateRunes(c.in, c.limit); got != c.want {
			t.Errorf("TruncateRunes(%q, %d) = %q, want %q", c.in, c.limit, got, c.want)
		}
	}
}

// A missing timestamp must stay zero rather than becoming now(), which would
// sort an undated turn ahead of everything real.
func TestParseTimestampLeavesAMissingTimeZero(t *testing.T) {
	if got := ParseTimestamp("", "   not a time"); !got.IsZero() {
		t.Fatalf("got %v, want the zero time", got)
	}
}

func TestParseTimestampTakesTheFirstUsableCandidate(t *testing.T) {
	got := ParseTimestamp("", "2026-01-02T03:04:05Z", "2020-01-01T00:00:00Z")
	if got.Year() != 2026 || got.Month() != 1 || got.Day() != 2 {
		t.Fatalf("got %v, want 2026-01-02", got)
	}
}
