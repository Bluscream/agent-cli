package search

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agentcli.local/ai/internal/idutil"
	_ "agentcli.local/ai/internal/provider/claude"
)

func TestMatchTerms(t *testing.T) {
	t.Parallel()
	const title = "pull vrcnext and check if our plugin system is still fully compatible"

	for _, tc := range []struct {
		name          string
		text, query   string
		caseSensitive bool
		want          bool
	}{
		{"all terms out of order", title, "vrcnext plugin", false, true},
		{"phrase that is not a substring", title, "plugin vrcnext", false, true},
		{"single term", title, "vrcnext", false, true},
		{"one term missing", title, "vrcnext dayz", false, false},
		{"case folded by default", title, "VRCNext PLUGIN", false, true},
		{"case sensitive rejects", title, "VRCNext", true, false},
		{"case sensitive accepts", title, "vrcnext", true, true},
		{"empty query matches nothing", title, "   ", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := MatchTerms(tc.text, tc.query, tc.caseSensitive); got != tc.want {
				t.Errorf("MatchTerms(%q, %q, %v) = %v, want %v", tc.text, tc.query, tc.caseSensitive, got, tc.want)
			}
		})
	}
}

// A multi-word query used to be matched only as one contiguous phrase, so a
// remembered title that reordered or interleaved the words found nothing.
func TestFuzzyMatcherFindsReorderedTerms(t *testing.T) {
	t.Parallel()
	const title = "pull vrcnext and check if our plugin system is still fully compatible"

	phrase, err := NewMatcher("vrcnext plugin", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if matched, _ := phrase.FindMatch(title, 140); matched {
		t.Error("phrase mode matched words that are not adjacent")
	}

	fuzzy, err := NewMatcher("vrcnext plugin", "", false, true)
	if err != nil {
		t.Fatal(err)
	}
	matched, snippet := fuzzy.FindMatch(title, 140)
	if !matched {
		t.Fatal("fuzzy mode missed a title containing every term")
	}
	if snippet == "" {
		t.Error("fuzzy match produced an empty snippet")
	}
	if !fuzzy.QuickBytesMatch([]byte(title)) {
		t.Error("QuickBytesMatch disagreed with FindMatch in fuzzy mode")
	}
	if fuzzy.QuickBytesMatch([]byte("pull vrcnext and check the bundle")) {
		t.Error("fuzzy mode matched text missing one of the terms")
	}
}

// QuickBytesMatch lowered the haystack but not the needle, so a case-sensitive
// search for text containing capitals could never match and the transcript was
// skipped before its turns were ever read.
func TestQuickBytesMatchHonoursCaseSensitivity(t *testing.T) {
	t.Parallel()
	const body = "the Deploy step rebuilds the bundle"

	sensitive, err := NewMatcher("Deploy", "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !sensitive.QuickBytesMatch([]byte(body)) {
		t.Error("case-sensitive query missed text with the exact same casing")
	}
	if sensitive.QuickBytesMatch([]byte("the deploy step rebuilds the bundle")) {
		t.Error("case-sensitive query matched differently-cased text")
	}

	insensitive, err := NewMatcher("DEPLOY", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !insensitive.QuickBytesMatch([]byte(body)) {
		t.Error("case-insensitive query missed differently-cased text")
	}
}

// Search results reported only the 8-character short ID, so a caller that
// wanted the canonical conversation ID had to resolve it with a second command.
func TestConversationResultsCarryFullID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude/projects/-tmp")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}

	const id = "7d317bcf-b662-4633-a20d-98f02a701007"
	line, err := json.Marshal(map[string]any{
		"type":    "user",
		"cwd":     "/tmp",
		"message": map[string]any{"role": "user", "content": "batch every change into one deploy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), append(line, '\n'), 0600); err != nil {
		t.Fatal(err)
	}

	results, err := Execute(Options{Text: "one deploy", Provider: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("no results for text present in the transcript")
	}
	for _, r := range results {
		if r.FullID != id {
			t.Errorf("FullID = %q, want the raw conversation ID %q", r.FullID, id)
		}
		if r.EntityID != idutil.ShortID(id) {
			t.Errorf("EntityID = %q, want the short ID %q", r.EntityID, idutil.ShortID(id))
		}
	}
}
