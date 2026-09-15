package claude

import (
	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (p *ClaudeProvider) ListMemories() ([]provider.MemoryItem, error) {
	home, _ := os.UserHomeDir()
	var items []provider.MemoryItem

	addMD := func(path, id, title string) {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		fi, _ := os.Stat(path)
		modTime := time.Now()
		if fi != nil {
			modTime = fi.ModTime()
		}
		items = append(items, provider.MemoryItem{
			Provider:   p.Name(),
			ID:         id,
			Title:      title,
			Content:    string(data),
			SourceFile: path,
			CreatedAt:  modTime,
			UpdatedAt:  modTime,
		})
	}

	// 1. Global memory file: ~/.claude/CLAUDE.md
	addMD(
		filepath.Join(home, ".claude/CLAUDE.md"),
		"global-claude-md",
		"Global Claude Instructions (~/.claude/CLAUDE.md)",
	)

	// 2. Per-project memory files: ~/.claude/projects/<slug>/memory/*.md
	// The slug is the workspace path with slashes replaced by dashes.
	projectsDir := filepath.Join(home, ".claude/projects")
	projEntries, err := os.ReadDir(projectsDir)
	if err == nil {
		for _, proj := range projEntries {
			if !proj.IsDir() {
				continue
			}
			memDir := filepath.Join(projectsDir, proj.Name(), "memory")
			memEntries, err := os.ReadDir(memDir)
			if err != nil {
				continue
			}
			// Convert slug back to a readable workspace label.
			workspaceLabel := strings.ReplaceAll(proj.Name(), "-", "/")
			for _, mem := range memEntries {
				if mem.IsDir() || !strings.HasSuffix(mem.Name(), ".md") {
					continue
				}
				memPath := filepath.Join(memDir, mem.Name())
				id := "project-" + proj.Name() + "-" + strings.TrimSuffix(mem.Name(), ".md")
				title := fmt.Sprintf("%s (%s)", strings.TrimSuffix(mem.Name(), ".md"), workspaceLabel)
				addMD(memPath, id, title)
			}
		}
	}

	return items, nil
}

func (p *ClaudeProvider) BackupMemories(destDir string) (string, error) {
	items, err := p.ListMemories()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}
	destFile := filepath.Join(destDir, fmt.Sprintf("claude-memories-%d.json", time.Now().Unix()))
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(destFile, data, 0644); err != nil {
		return "", err
	}
	return destFile, nil
}

func (p *ClaudeProvider) PurgeMemories() (int, error) {
	items, err := p.ListMemories()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, it := range items {
		if it.SourceFile != "" {
			if err := os.Remove(it.SourceFile); err == nil {
				count++
			}
		}
	}
	return count, nil
}

func (p *ClaudeProvider) ImportMemory(item provider.MemoryItem) error {
	home, _ := os.UserHomeDir()
	slug := idutil.ShortID(item.ID)
	if slug == "" {
		slug = fmt.Sprintf("imported-%d", time.Now().Unix())
	}
	memDir := filepath.Join(home, ".claude/projects/-imported-shared/memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		return err
	}
	destFile := filepath.Join(memDir, slug+".md")
	content := fmt.Sprintf("# %s\n\n%s\n", item.Title, item.Content)
	return os.WriteFile(destFile, []byte(content), 0644)
}
