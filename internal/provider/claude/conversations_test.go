package claude

import (
	"encoding/json"
	"fmt"
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
	turns, msgCount := readClaudeTurns(path, 0)
	if len(turns) != 1 || turns[0].Content != strings.TrimSpace(content) {
		t.Fatal("message truncated")
	}
	if msgCount != 1 {
		t.Fatalf("message count = %d, want 1", msgCount)
	}
}

// Listing stops reading a transcript as soon as it has the title and
// workspace, so it used to report the position the scan stopped at (10, or 40)
// as though it were the message total. A partial scan must report
// CountUnknown, and the detail view must report the real figure.
func TestMessagesCountIsNeverThePartialScanPosition(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude/projects/-tmp")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}

	const id = "abcdef01-2345-6789-abcd-ef0123456789"
	const records = 137
	var transcript strings.Builder
	for i := range records {
		role, key := "user", "message"
		if i%2 == 1 {
			role = "assistant"
		}
		line, err := json.Marshal(map[string]any{
			"type": role,
			"cwd":  "/tmp",
			key:    map[string]any{"role": role, "content": fmt.Sprintf("record %d", i)},
		})
		if err != nil {
			t.Fatal(err)
		}
		transcript.Write(line)
		transcript.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(transcript.String()), 0600); err != nil {
		t.Fatal(err)
	}

	summaries, err := New().ListConversations(provider.HistoryOptions{})
	if err != nil || len(summaries) != 1 {
		t.Fatalf("summaries: %+v %v", summaries, err)
	}
	if got := summaries[0].MessagesCount; got != provider.CountUnknown {
		t.Errorf("listing reported %d messages after a partial scan, want CountUnknown (%d)", got, provider.CountUnknown)
	}

	detail, err := New().GetConversation(id)
	if err != nil {
		t.Fatal(err)
	}
	if got := detail.Summary.MessagesCount; got != records {
		t.Errorf("detail reported %d messages, want the true %d", got, records)
	}
}
