package codex

import (
	"fmt"
	"os"
	"path/filepath"

	"agentcli.local/ai/internal/provider"
	"agentcli.local/ai/internal/sqlite"
)

// threadTables are the tables keyed on a thread id, deleted from in this order
// so children go before the row they reference.
//
// thread_dynamic_tools and thread_artifacts declare ON DELETE CASCADE, but
// SQLite only enforces a foreign key when PRAGMA foreign_keys is on, and it is
// off by default. Relying on the cascade would silently orphan rows.
var threadTables = []struct {
	Table  string
	Column string
}{
	{"thread_artifacts", "thread_id"},
	{"thread_dynamic_tools", "thread_id"},
	{"thread_spawn_edges", "child_thread_id"},
	{"thread_spawn_edges", "parent_thread_id"},
	{"threads", "id"},
}

func statePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex/state_5.sqlite"), nil
}

// PurgeConversation implements provider.ConversationPurger.
//
// A Codex conversation is a rollout JSONL on disk plus rows across the thread
// tables in state_5.sqlite. Deleting only the file would leave `ai history`
// listing a thread whose transcript cannot be read.
func (p *CodexProvider) PurgeConversation(id string, opts provider.PurgeOptions) (*provider.Purge, error) {
	summary, err := provider.Find(p, id)
	if err != nil {
		return nil, err
	}
	dbPath, err := statePath()
	if err != nil {
		return nil, err
	}
	purge := p.purgeSummary(dbPath, *summary, opts)
	return &purge, nil
}

// PurgeAllConversations implements provider.ConversationPurger.
func (p *CodexProvider) PurgeAllConversations(opts provider.PurgeOptions) ([]provider.Purge, error) {
	summaries, err := p.ListConversations(provider.HistoryOptions{})
	if err != nil {
		return nil, err
	}
	dbPath, err := statePath()
	if err != nil {
		return nil, err
	}
	purges := make([]provider.Purge, 0, len(summaries))
	for _, summary := range summaries {
		purges = append(purges, p.purgeSummary(dbPath, summary, opts))
	}
	return purges, nil
}

func (p *CodexProvider) purgeSummary(dbPath string, summary provider.ConversationSummary, opts provider.PurgeOptions) provider.Purge {
	purge := provider.Purge{
		Provider: p.Name(),
		ID:       summary.ID,
		Title:    summary.Title,
		DryRun:   opts.DryRun,
	}

	home, err := os.UserHomeDir()
	if err != nil {
		purge.Warnf("resolving home directory: %v", err)
		return purge
	}

	// The rollout path comes out of the database, so it is confined to the
	// provider's own directory rather than followed wherever it points.
	if summary.TranscriptPath != "" {
		purge.RemovePath(filepath.Join(home, ".codex"), summary.TranscriptPath, opts)
	}

	rows, err := purgeThreadRows(dbPath, summary.ID, opts.DryRun)
	if err != nil {
		purge.Warnf("%v", err)
		return purge
	}
	purge.RemovedRows = rows
	// The threads row is this provider's conversation index, so its removal is
	// what stops the conversation being listed.
	if rows > 0 {
		purge.RemovedIndexEntries = 1
	}

	if len(purge.RemovedPaths) == 0 && rows == 0 {
		purge.Warnf("found no stored data for conversation %s", summary.ID)
	}
	return purge
}

// purgeThreadRows deletes a thread from every table keyed on it, in one
// transaction, and returns how many rows went. A dry run counts the same rows
// without deleting them.
func purgeThreadRows(dbPath, threadID string, dryRun bool) (int, error) {
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	if dryRun {
		total := 0
		for _, t := range threadTables {
			var rows []struct {
				N int `json:"n"`
			}
			q := fmt.Sprintf("SELECT COUNT(*) AS n FROM %s WHERE %s = ?;", t.Table, t.Column)
			if err := sqlite.Query(dbPath, q, &rows, threadID); err != nil {
				return total, fmt.Errorf("counting %s rows for thread %s: %w", t.Table, threadID, err)
			}
			if len(rows) > 0 {
				total += rows[0].N
			}
		}
		return total, nil
	}

	stmt := "BEGIN IMMEDIATE;\n"
	args := make([]string, 0, len(threadTables))
	for _, t := range threadTables {
		stmt += fmt.Sprintf("DELETE FROM %s WHERE %s = ?;\n", t.Table, t.Column)
		args = append(args, threadID)
	}
	stmt += "COMMIT;\n"

	rows, err := sqlite.ExecCount(dbPath, stmt, args...)
	if err != nil {
		return 0, fmt.Errorf("deleting thread %s from %s: %w", threadID, filepath.Base(dbPath), err)
	}
	return rows, nil
}

var _ provider.ConversationPurger = (*CodexProvider)(nil)
