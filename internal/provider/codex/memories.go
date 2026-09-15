package codex

import (
	"agentcli.local/ai/internal/provider"
	"agentcli.local/ai/internal/sqlite"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

	cmd := exec.Command("sqlite3", dbPath, fmt.Sprintf(".backup '%s'", destFile))
	if err := cmd.Run(); err != nil {
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

	cmd := exec.Command("sqlite3", dbPath, "DELETE FROM stage1_outputs; SELECT changes();")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("failed to purge memories: %w", err)
	}

	cnt, _ := strconv.Atoi(strings.TrimSpace(out.String()))
	return cnt, nil
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

	// Escape single quotes for SQLite
	safeRaw := strings.ReplaceAll(item.Content, "'", "''")
	safeSummary := strings.ReplaceAll(title, "'", "''")
	safeTid := strings.ReplaceAll(tid, "'", "''")

	query := fmt.Sprintf(
		"INSERT OR REPLACE INTO stage1_outputs (thread_id, source_updated_at, raw_memory, rollout_summary, rollout_slug, generated_at) VALUES ('%s', %d, '%s', '%s', '%s', %d);",
		safeTid, nowSec, safeRaw, safeSummary, safeSummary, nowSec,
	)
	cmd := exec.Command("sqlite3", dbPath, query)
	return cmd.Run()
}
