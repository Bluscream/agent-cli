package claude

import (
	"os"
	"path/filepath"
	"testing"

	"agentcli.local/ai/internal/provider"
)

// seedClaudeConversation builds a fixture Claude home: a transcript, its
// sidecar directory, and a desktop session file naming the same id. Every test
// here works inside t.TempDir; none of them touch the real ~/.claude.
func seedClaudeConversation(t *testing.T, home, workspace, id string) (transcript, sidecar, session string) {
	t.Helper()

	projectDir := filepath.Join(home, ".claude/projects", workspace)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	transcript = filepath.Join(projectDir, id+".jsonl")
	line := `{"type":"user","message":{"role":"user","content":"hello"},"timestamp":"2026-01-01T00:00:00Z"}` + "\n"
	if err := os.WriteFile(transcript, []byte(line), 0600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	sidecar = filepath.Join(projectDir, id)
	if err := os.MkdirAll(sidecar, 0755); err != nil {
		t.Fatalf("mkdir sidecar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sidecar, "custom-title.json"), []byte(`{"title":"fixture"}`), 0600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	sessionDir := filepath.Join(home, ".config/Claude/claude-code-sessions", "outer", "inner")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	session = filepath.Join(sessionDir, "local_fixture.json")
	if err := os.WriteFile(session, []byte(`{"sessionId":"`+id+`","title":"fixture"}`), 0600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	return transcript, sidecar, session
}

func TestPurgeConversationRemovesTranscriptSidecarAndSessionFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	transcript, sidecar, session := seedClaudeConversation(t, home, "-tmp-fixture", id)

	p := &ClaudeProvider{}
	purge, err := p.PurgeConversation(id, provider.PurgeOptions{})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if !purge.Complete() {
		t.Fatalf("unexpected warnings: %v", purge.Warnings)
	}
	for _, path := range []string{transcript, sidecar, session} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s survived the purge: %v", path, err)
		}
	}

	// The conversation must no longer be listed: a transcript deleted without
	// its index entry is exactly the half-delete this is meant to avoid.
	remaining, err := p.ListConversations(provider.HistoryOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, c := range remaining {
		if c.ID == id {
			t.Fatal("the purged conversation is still listed")
		}
	}
}

func TestPurgeConversationDryRunRemovesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	transcript, sidecar, session := seedClaudeConversation(t, home, "-tmp-fixture", id)

	p := &ClaudeProvider{}
	purge, err := p.PurgeConversation(id, provider.PurgeOptions{DryRun: true})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if len(purge.RemovedPaths) != 3 {
		t.Fatalf("dry run listed %d paths, want 3: %v", len(purge.RemovedPaths), purge.RemovedPaths)
	}
	if purge.FreedBytes <= 0 {
		t.Fatalf("dry run measured %d bytes", purge.FreedBytes)
	}
	for _, path := range []string{transcript, sidecar, session} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("dry run deleted %s: %v", path, err)
		}
	}
}

func TestPurgeConversationRejectsAnUnknownID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := &ClaudeProvider{}
	if _, err := p.PurgeConversation("not-a-real-id", provider.PurgeOptions{}); err == nil {
		t.Fatal("expected an error for an id that matches nothing")
	}
}

func TestPurgeAllConversationsEmptiesTheProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	seedClaudeConversation(t, home, "-tmp-one", "11111111-1111-1111-1111-111111111111")
	seedClaudeConversation(t, home, "-tmp-two", "22222222-2222-2222-2222-222222222222")

	p := &ClaudeProvider{}
	purges, err := p.PurgeAllConversations(provider.PurgeOptions{})
	if err != nil {
		t.Fatalf("purge all: %v", err)
	}
	if len(purges) != 2 {
		t.Fatalf("purged %d conversations, want 2", len(purges))
	}
	for _, purge := range purges {
		if !purge.Complete() {
			t.Fatalf("purge of %s warned: %v", purge.ID, purge.Warnings)
		}
	}

	remaining, err := p.ListConversations(provider.HistoryOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("%d conversations survived a full purge", len(remaining))
	}
}
