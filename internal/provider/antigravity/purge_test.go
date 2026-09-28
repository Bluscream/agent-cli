package antigravity

import (
	"bytes"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"agentcli.local/ai/internal/provider"
	"agentcli.local/ai/internal/sqlite"
)

// seedAntigravity builds a fixture Antigravity home: a state.vscdb holding a
// trajectory index over the given ids, and for each one a brain directory with
// a transcript and a per-conversation database. All under t.TempDir.
func seedAntigravity(t *testing.T, home string, ids ...string) (dbPath string) {
	t.Helper()
	if _, err := exec.LookPath(sqlite.Binary()); err != nil {
		t.Skipf("sqlite3 not available: %v", err)
	}

	globalStorage := filepath.Join(home, ".config/Antigravity IDE/User/globalStorage")
	brainDir := filepath.Join(home, ".gemini/antigravity-ide/brain")
	convDir := filepath.Join(home, ".gemini/antigravity-ide/conversations")
	for _, dir := range []string{globalStorage, brainDir, convDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	index := new(bytes.Buffer)
	for _, id := range ids {
		entry, err := buildSummaryEntry(ConvoMeta{
			ID:           id,
			Title:        "fixture " + id[:8],
			TrajectoryID: id,
			WorkspaceURI: "file:///tmp/fixture",
			StepCount:    2,
			CreatedTS:    1700000000,
			ModifiedTS:   1700000100,
		})
		if err != nil {
			t.Fatalf("build entry: %v", err)
		}
		index.Write(entry)

		logDir := filepath.Join(brainDir, id, ".system_generated", "logs")
		if err := os.MkdirAll(logDir, 0755); err != nil {
			t.Fatalf("mkdir brain: %v", err)
		}
		if err := os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), []byte("{}\n"), 0600); err != nil {
			t.Fatalf("write transcript: %v", err)
		}
		if err := os.WriteFile(filepath.Join(brainDir, id, "notes.md"), []byte("artifact body"), 0600); err != nil {
			t.Fatalf("write artifact: %v", err)
		}
		if err := os.WriteFile(filepath.Join(convDir, id+".db"), []byte("conversation db"), 0600); err != nil {
			t.Fatalf("write conversation db: %v", err)
		}
	}

	dbPath = filepath.Join(globalStorage, "state.vscdb")
	if err := sqlite.Exec(dbPath, "CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value BLOB);"); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := sqlite.Exec(dbPath, "INSERT INTO ItemTable VALUES(?, ?);",
		trajectorySummariesKey, base64.StdEncoding.EncodeToString(index.Bytes())); err != nil {
		t.Fatalf("seed index: %v", err)
	}
	return dbPath
}

// indexIDs reads the ids currently in the trajectory index.
//
// An index with every entry removed encodes as an empty value, which reads back
// the same as a missing key. That is the right answer for the callers — both
// mean "no conversations are indexed" — so it counts as an empty index here
// rather than a failure.
func indexIDs(t *testing.T, dbPath string) []string {
	t.Helper()
	raw, err := readVscdbB64Proto(dbPath, trajectorySummariesKey)
	if err != nil {
		return nil
	}
	entries, err := extractMapEntries(raw)
	if err != nil {
		t.Fatalf("parse index: %v", err)
	}
	ids := make([]string, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	return ids
}

const (
	fixtureA = "aaaaaaaa-1111-2222-3333-444444444444"
	fixtureB = "bbbbbbbb-1111-2222-3333-444444444444"
)

// The index entry is what the IDE lists from. Removing the files without it
// leaves a conversation that appears in the list and opens to nothing, which is
// the half-delete this whole capability exists to avoid.
func TestPurgeConversationRemovesFilesAndTheIndexEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := seedAntigravity(t, home, fixtureA, fixtureB)

	p := &AntigravityProvider{}
	purge, err := p.PurgeConversation(fixtureA, provider.PurgeOptions{})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if !purge.Complete() {
		t.Fatalf("unexpected warnings: %v", purge.Warnings)
	}
	if purge.RemovedIndexEntries != 1 {
		t.Fatalf("index entries removed: %d, want 1", purge.RemovedIndexEntries)
	}

	for _, path := range []string{
		filepath.Join(home, ".gemini/antigravity-ide/brain", fixtureA),
		filepath.Join(home, ".gemini/antigravity-ide/conversations", fixtureA+".db"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s survived: %v", path, err)
		}
	}

	// The other conversation must be untouched, index entry included.
	ids := indexIDs(t, dbPath)
	if len(ids) != 1 || ids[0] != fixtureB {
		t.Fatalf("index holds %v, want just the other conversation", ids)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini/antigravity-ide/brain", fixtureB)); err != nil {
		t.Fatalf("purging one conversation removed the other: %v", err)
	}
}

// The backup taken before editing the index must not be left behind: it holds
// a copy of the very data the user asked to erase.
func TestPurgeConversationLeavesNoIndexBackupByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := seedAntigravity(t, home, fixtureA)

	p := &AntigravityProvider{}
	purge, err := p.PurgeConversation(fixtureA, provider.PurgeOptions{})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if purge.BackupPath != "" {
		t.Fatalf("a backup path was reported without --keep-backup: %s", purge.BackupPath)
	}

	entries, err := os.ReadDir(filepath.Dir(dbPath))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".vscdb" && len(e.Name()) > len("state.vscdb") {
			t.Fatalf("a backup of the state database was left behind: %s", e.Name())
		}
	}
}

func TestPurgeConversationKeepsTheBackupWhenAsked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	seedAntigravity(t, home, fixtureA)

	p := &AntigravityProvider{}
	purge, err := p.PurgeConversation(fixtureA, provider.PurgeOptions{KeepBackup: true})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if purge.BackupPath == "" {
		t.Fatal("no backup path reported with KeepBackup")
	}
	if _, err := os.Stat(purge.BackupPath); err != nil {
		t.Fatalf("the reported backup does not exist: %v", err)
	}
}

func TestPurgeConversationDryRunChangesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := seedAntigravity(t, home, fixtureA, fixtureB)

	p := &AntigravityProvider{}
	purge, err := p.PurgeConversation(fixtureA, provider.PurgeOptions{DryRun: true})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if purge.RemovedIndexEntries != 1 {
		t.Fatalf("dry run reported %d index entries, want 1", purge.RemovedIndexEntries)
	}
	if len(purge.RemovedPaths) != 2 {
		t.Fatalf("dry run reported %v", purge.RemovedPaths)
	}
	if len(indexIDs(t, dbPath)) != 2 {
		t.Fatal("dry run edited the index")
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini/antigravity-ide/brain", fixtureA)); err != nil {
		t.Fatalf("dry run deleted the brain directory: %v", err)
	}
}

func TestPurgeAllConversationsEmptiesTheIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := seedAntigravity(t, home, fixtureA, fixtureB)

	p := &AntigravityProvider{}
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
	if ids := indexIDs(t, dbPath); len(ids) != 0 {
		t.Fatalf("index still holds %v", ids)
	}

	remaining, err := p.ListConversations(provider.HistoryOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("%d conversations survived a full purge", len(remaining))
	}
}

func TestPurgeConversationRejectsAnUnknownID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	seedAntigravity(t, home, fixtureA)

	p := &AntigravityProvider{}
	if _, err := p.PurgeConversation("cccccccc-0000-0000-0000-000000000000", provider.PurgeOptions{}); err == nil {
		t.Fatal("expected an error for an id that matches nothing")
	}
}
