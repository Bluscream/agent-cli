package codex

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentcli.local/ai/internal/provider"
)

func TestMultilineDatabaseRows(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 unavailable")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".codex")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, sql := range map[string]string{
		"state_5.sqlite":    "CREATE TABLE threads(id,title,created_at,updated_at,cwd,model,rollout_path); INSERT INTO threads VALUES('x','line1'||char(10)||'line2',1,2,'/tmp','model','');",
		"memories_1.sqlite": "CREATE TABLE stage1_outputs(thread_id,raw_memory,rollout_summary,rollout_slug,generated_at); INSERT INTO stage1_outputs VALUES('x','line1'||char(10)||'line2','summary','slug',1);",
	} {
		if out, err := exec.Command("sqlite3", filepath.Join(dir, name), sql).CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v %s", err, out)
		}
	}
	conversations, err := New().ListConversations(provider.HistoryOptions{})
	if err != nil || len(conversations) != 1 || conversations[0].Title != "line1\nline2" {
		t.Fatalf("conversations: %+v, %v", conversations, err)
	}
	memories, err := New().ListMemories()
	if err != nil || len(memories) != 1 || memories[0].Content != "line1\nline2" {
		t.Fatalf("memories: %+v, %v", memories, err)
	}
}

func TestRolloutRetainsFullContent(t *testing.T) {
	content := strings.Repeat("long message 🐈\n", 100)
	line, err := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{"role": "user", "content": []map[string]string{{"type": "input_text", "text": content}}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, append(line, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	turns, _ := readRolloutTurns(path, 0)
	if len(turns) != 1 || turns[0].Content != strings.TrimSpace(content) {
		t.Fatal("message lost or truncated")
	}
}
