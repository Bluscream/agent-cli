package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"agentcli.local/ai/internal/provider"
	"agentcli.local/ai/internal/sqlite"
)

func (p *CodexProvider) ListMemories() ([]provider.MemoryItem, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".codex/memories_1.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nil
	}

	query := "SELECT thread_id, raw_memory, rollout_summary, rollout_slug, generated_at FROM stage1_outputs ORDER BY generated_at DESC;"
	var rows []struct {
		ThreadID    string `json:"thread_id"`
		Raw         string `json:"raw_memory"`
		Summary     string `json:"rollout_summary"`
		Slug        string `json:"rollout_slug"`
		GeneratedAt int64  `json:"generated_at"`
	}
	if err := sqlite.Query(dbPath, query, &rows); err != nil {
		return nil, err
	}
	items := []provider.MemoryItem{}
	for _, row := range rows {
		tid, raw, summary, slug := row.ThreadID, row.Raw, row.Summary, row.Slug
		t := time.Unix(row.GeneratedAt, 0)

		title := slug
		if title == "" {
			title = summary
		}
		if title == "" {
			title = fmt.Sprintf("Memory for thread %.8s", tid)
		}

		items = append(items, provider.MemoryItem{
			Provider:   p.Name(),
			ID:         tid,
			Title:      title,
			Content:    raw,
			CreatedAt:  t,
			UpdatedAt:  t,
			SourceFile: dbPath,
		})
	}

	return items, nil
}

func (p *CodexProvider) BackupMemories(destDir string) (string, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".codex/memories_1.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return "", fmt.Errorf("codex memories db not found at %s", dbPath)
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}
	destFile := filepath.Join(destDir, fmt.Sprintf("codex-memories-%d.sqlite", time.Now().Unix()))

	if err := sqlite.Backup(dbPath, destFile); err != nil {
		return "", fmt.Errorf("sqlite backup failed: %w", err)
	}
	return destFile, nil
}

func (p *CodexProvider) PurgeMemories() (int, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".codex/memories_1.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return 0, nil
	}

	// changes() is read back so the caller reports what was actually deleted
	// rather than assuming the delete applied.
	rows, err := sqlite.ExecCount(dbPath, "DELETE FROM stage1_outputs;")
	if err != nil {
		return 0, fmt.Errorf("failed to purge memories: %w", err)
	}
	return rows, nil
}

func (p *CodexProvider) ImportMemory(item provider.MemoryItem) error {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".codex/memories_1.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("codex memories sqlite db not found: %s", dbPath)
	}

	tid := item.ID
	if tid == "" {
		tid = fmt.Sprintf("imported-%d", time.Now().Unix())
	}
	title := item.Title
	if title == "" {
		title = item.ID
	}
	nowSec := time.Now().Unix()

	const q = `INSERT OR REPLACE INTO stage1_outputs
(thread_id, source_updated_at, raw_memory, rollout_summary, rollout_slug, generated_at)
VALUES (?, ?, ?, ?, ?, ?);`
	ts := strconv.FormatInt(nowSec, 10)
	return sqlite.Exec(dbPath, q, tid, ts, item.Content, title, title, ts)
}
