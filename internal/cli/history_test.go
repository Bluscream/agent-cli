package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "agentcli.local/ai/internal/provider/claude"
)

// writeClaudeTranscript lays down a single-record Claude transcript whose first
// user message becomes the conversation title.
func writeClaudeTranscript(t *testing.T, home, id, firstMessage string) {
	t.Helper()
	dir := filepath.Join(home, ".claude/projects/-tmp")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(map[string]any{
		"type":    "user",
		"cwd":     "/tmp",
		"message": map[string]any{"role": "user", "content": firstMessage},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), append(line, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func runHistory(t *testing.T, args ...string) string {
	t.Helper()
	out := &bytes.Buffer{}
	cmd := New(&bytes.Buffer{}, out, &bytes.Buffer{})
	cmd.SetArgs(append([]string{"history", "--provider=claude", "--color=never"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// historyTitles reads the titles from JSON output, which the table's column
// wrapping would otherwise break apart.
func historyTitles(t *testing.T, args ...string) []string {
	t.Helper()
	raw := []byte(runHistory(t, append(args, "--output=json")...))
	// Debug-tagged builds wrap the payload as {"_debug":{...},"data":[...]}.
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err == nil {
		if data, ok := outer["data"]; ok {
			raw = data
		}
	}
	var convos []struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(raw, &convos); err != nil {
		t.Fatalf("decoding history JSON: %v (%s)", err, raw)
	}
	titles := make([]string, 0, len(convos))
	for _, c := range convos {
		titles = append(titles, c.Title)
	}
	return titles
}

// A remembered title is rarely a contiguous substring of the real one, so the
// keyword filter matches every word in any order instead.
func TestHistoryKeywordMatchesTermsInAnyOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEBUG", "0")
	t.Setenv("AI_DEBUG", "0")
	const title = "pull vrcnext and check if our plugin system is still fully compatible"
	writeClaudeTranscript(t, home, "7d317bcf-b662-4633-a20d-98f02a701007", title)

	for _, keyword := range []string{"vrcnext plugin", "plugin vrcnext", "vrcnext", "PLUGIN system"} {
		got := historyTitles(t, "-k", keyword)
		if len(got) != 1 || got[0] != title {
			t.Errorf("keyword %q matched %v, want the one conversation", keyword, got)
		}
	}

	// A term that is genuinely absent still excludes the conversation.
	if got := historyTitles(t, "-k", "vrcnext dayz"); len(got) != 0 {
		t.Errorf("keyword with an absent term matched %v, want nothing", got)
	}
}

// A title-only search that finds nothing used to render an empty table, giving
// no sign that the transcripts themselves are searchable with another command.
func TestHistoryKeywordMissSuggestsContentSearch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEBUG", "0")
	t.Setenv("AI_DEBUG", "0")
	writeClaudeTranscript(t, home, "7d317bcf-b662-4633-a20d-98f02a701007",
		"pull vrcnext and check if our plugin system is still fully compatible")

	got := runHistory(t, "-k", "vrcnext local")
	if !strings.Contains(got, "No conversation title matches") {
		t.Errorf("missing explanation for the empty result: %s", got)
	}
	if !strings.Contains(got, `ai search "vrcnext local"`) {
		t.Errorf("missing the content-search suggestion: %s", got)
	}
}
