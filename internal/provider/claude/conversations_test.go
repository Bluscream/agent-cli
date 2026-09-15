package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentcli.local/ai/internal/provider"
)

func TestShortFilenameAndLongTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude/projects/-tmp")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("message 🐈\n", 100)
	data, err := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": content}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "x.jsonl")
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	summaries, err := New().ListConversations(provider.HistoryOptions{})
	if err != nil || len(summaries) != 1 {
		t.Fatalf("summaries: %+v %v", summaries, err)
	}
	turns := readClaudeTurns(path, 0)
	if len(turns) != 1 || turns[0].Content != strings.TrimSpace(content) {
		t.Fatal("message truncated")
	}
}
