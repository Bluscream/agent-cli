package codex

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"agentcli.local/ai/internal/provider"
	"agentcli.local/ai/internal/sqlite"
)

// seedCodexState builds a fixture ~/.codex: a state database with one thread
// and its artifacts, plus the rollout JSONL the thread points at. Everything
// lives under t.TempDir; the real ~/.codex is never touched.
func seedCodexState(t *testing.T, home, id string) (dbPath, rollout string) {
	t.Helper()
	if _, err := exec.LookPath(sqlite.Binary()); err != nil {
		t.Skipf("sqlite3 not available: %v", err)
	}

	codexDir := filepath.Join(home, ".codex")
	sessionsDir := filepath.Join(codexDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	rollout = filepath.Join(sessionsDir, id+".jsonl")
	line := `{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}` + "\n"
	if err := os.WriteFile(rollout, []byte(line), 0600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	dbPath = filepath.Join(codexDir, "state_5.sqlite")
	schema := `
CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT, created_at INTEGER, updated_at INTEGER, cwd TEXT, model TEXT, rollout_path TEXT);
CREATE TABLE thread_artifacts (id TEXT PRIMARY KEY, thread_id TEXT, artifact_type TEXT, identity_key TEXT, payload TEXT, created_at INTEGER);
CREATE TABLE thread_dynamic_tools (thread_id TEXT, position INTEGER, name TEXT, description TEXT, input_schema TEXT);
CREATE TABLE thread_spawn_edges (parent_thread_id TEXT, child_thread_id TEXT PRIMARY KEY, status TEXT);
`
	if err := sqlite.Exec(dbPath, schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := sqlite.Exec(dbPath,
		"INSERT INTO threads VALUES(?, ?, 1700000000, 1700000001, ?, 'gpt-test', ?);",
		id, "fixture thread", "/tmp/fixture", rollout); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	if err := sqlite.Exec(dbPath,
		"INSERT INTO thread_artifacts VALUES('art-1', ?, 'file', 'notes.md', 'body', 1700000002);", id); err != nil {
		t.Fatalf("insert artifact: %v", err)
	}
	if err := sqlite.Exec(dbPath,
		"INSERT INTO thread_dynamic_tools VALUES(?, 0, 'tool', 'desc', '{}');", id); err != nil {
		t.Fatalf("insert tool: %v", err)
	}
	return dbPath, rollout
}

func rowCount(t *testing.T, dbPath, table, column, id string) int {
	t.Helper()
	var rows []struct {
		N int `json:"n"`
	}
	q := fmt.Sprintf("SELECT COUNT(*) AS n FROM %s WHERE %s = ?;", table, column)
	if err := sqlite.Query(dbPath, q, &rows, id); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if len(rows) == 0 {
		return 0
	}
	return rows[0].N
}

func TestPurgeConversationRemovesRolloutAndEveryThreadRow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	id := "01999999-aaaa-bbbb-cccc-dddddddddddd"
	dbPath, rollout := seedCodexState(t, home, id)

	p := &CodexProvider{}
	purge, err := p.PurgeConversation(id, provider.PurgeOptions{})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if !purge.Complete() {
		t.Fatalf("unexpected warnings: %v", purge.Warnings)
	}
	if _, err := os.Stat(rollout); !os.IsNotExist(err) {
		t.Fatalf("the rollout file survived: %v", err)
	}

	// Every table keyed on the thread must be empty. SQLite does not enforce
	// ON DELETE CASCADE unless PRAGMA foreign_keys is on, so relying on the
	// cascade would leave these rows behind.
	for _, table := range []struct{ name, column string }{
		{"threads", "id"},
		{"thread_artifacts", "thread_id"},
		{"thread_dynamic_tools", "thread_id"},
	} {
		if n := rowCount(t, dbPath, table.name, table.column, id); n != 0 {
			t.Fatalf("%s still holds %d row(s) for the purged thread", table.name, n)
		}
	}
	if purge.RemovedIndexEntries != 1 {
		t.Fatalf("index entries removed: %d, want 1", purge.RemovedIndexEntries)
	}

	remaining, err := p.ListConversations(provider.HistoryOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("%d conversations survived", len(remaining))
	}
}

func TestPurgeConversationDryRunLeavesTheDatabaseIntact(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	id := "01999999-aaaa-bbbb-cccc-dddddddddddd"
	dbPath, rollout := seedCodexState(t, home, id)

	p := &CodexProvider{}
	purge, err := p.PurgeConversation(id, provider.PurgeOptions{DryRun: true})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if purge.RemovedRows != 3 {
		t.Fatalf("dry run counted %d rows, want 3 (thread, artifact, tool)", purge.RemovedRows)
	}
	if _, err := os.Stat(rollout); err != nil {
		t.Fatalf("dry run deleted the rollout: %v", err)
	}
	if n := rowCount(t, dbPath, "threads", "id", id); n != 1 {
		t.Fatalf("dry run changed the database: threads holds %d row(s)", n)
	}
}

func TestPurgeAllConversationsEmptiesTheDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	first := "01999999-aaaa-bbbb-cccc-dddddddddddd"
	dbPath, _ := seedCodexState(t, home, first)

	second := "01888888-aaaa-bbbb-cccc-dddddddddddd"
	secondRollout := filepath.Join(home, ".codex/sessions", second+".jsonl")
	if err := os.WriteFile(secondRollout, []byte("{}\n"), 0600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	if err := sqlite.Exec(dbPath,
		"INSERT INTO threads VALUES(?, 'second', 1700000000, 1700000001, '/tmp', 'gpt-test', ?);",
		second, secondRollout); err != nil {
		t.Fatalf("insert: %v", err)
	}

	p := &CodexProvider{}
	purges, err := p.PurgeAllConversations(provider.PurgeOptions{})
	if err != nil {
		t.Fatalf("purge all: %v", err)
	}
	if len(purges) != 2 {
		t.Fatalf("purged %d conversations, want 2", len(purges))
	}
	var rows []struct {
		N int `json:"n"`
	}
	if err := sqlite.Query(dbPath, "SELECT COUNT(*) AS n FROM threads;", &rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows[0].N != 0 {
		t.Fatalf("%d thread(s) survived a full purge", rows[0].N)
	}
}

// A rollout_path pointing outside ~/.codex must not be followed: the value
// comes out of a database, and deleting an arbitrary path because a row said so
// is not something a delete of one conversation should do.
func TestPurgeRefusesARolloutPathOutsideTheCodexDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	id := "01999999-aaaa-bbbb-cccc-dddddddddddd"
	dbPath, _ := seedCodexState(t, home, id)

	outside := filepath.Join(t.TempDir(), "elsewhere.jsonl")
	if err := os.WriteFile(outside, []byte("{}\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := sqlite.Exec(dbPath, "UPDATE threads SET rollout_path = ? WHERE id = ?;", outside, id); err != nil {
		t.Fatalf("update: %v", err)
	}

	p := &CodexProvider{}
	purge, err := p.PurgeConversation(id, provider.PurgeOptions{})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("a file outside ~/.codex was deleted: %v", err)
	}
	if purge.Complete() {
		t.Fatal("expected a warning about the path outside ~/.codex")
	}
}
