package antigravity

import (
	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (p *AntigravityProvider) ListMemories() ([]provider.MemoryItem, error) {
	home, _ := os.UserHomeDir()
	knowledgeDirs := []string{
		filepath.Join(home, ".gemini/antigravity-ide/knowledge"),
		filepath.Join(home, ".gemini/config/knowledge"),
		filepath.Join(home, ".gemini/knowledge"),
	}

	var items []provider.MemoryItem
	for _, kd := range knowledgeDirs {
		entries, err := os.ReadDir(kd)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			metaPath := filepath.Join(kd, e.Name(), "metadata.json")
			data, err := os.ReadFile(metaPath)
			if err != nil {
				continue
			}
			var meta struct {
				Summary   string `json:"summary"`
				Title     string `json:"title"`
				CreatedAt string `json:"created_at"`
				UpdatedAt string `json:"updated_at"`
			}
			_ = json.Unmarshal(data, &meta)

			fi, _ := e.Info()
			modTime := time.Now()
			if fi != nil {
				modTime = fi.ModTime()
			}

			title := meta.Title
			if title == "" {
				title = e.Name()
			}

			items = append(items, provider.MemoryItem{
				Provider:   p.Name(),
				ID:         e.Name(),
				Title:      title,
				Content:    meta.Summary,
				SourceFile: metaPath,
				CreatedAt:  modTime,
				UpdatedAt:  modTime,
			})
		}
	}
	return items, nil
}

func (p *AntigravityProvider) BackupMemories(destDir string) (string, error) {
	items, err := p.ListMemories()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}
	destFile := filepath.Join(destDir, fmt.Sprintf("antigravity-memories-%d.json", time.Now().Unix()))
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(destFile, data, 0644); err != nil {
		return "", err
	}
	return destFile, nil
}

func (p *AntigravityProvider) PurgeMemories() (int, error) {
	items, err := p.ListMemories()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, item := range items {
		if item.SourceFile != "" {
			parent := filepath.Dir(item.SourceFile)
			if err := os.RemoveAll(parent); err == nil {
				count++
			}
		}
	}
	return count, nil
}

func (p *AntigravityProvider) ImportMemory(item provider.MemoryItem) error {
	home, _ := os.UserHomeDir()
	slug := idutil.ShortID(item.ID)
	if slug == "" {
		slug = fmt.Sprintf("imported-%d", time.Now().Unix())
	}
	targetDir := filepath.Join(home, ".gemini/antigravity-ide/knowledge", slug)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}

	meta := map[string]any{
		"title":      item.Title,
		"summary":    item.Content,
		"created_at": item.CreatedAt.Format(time.RFC3339),
		"updated_at": item.UpdatedAt.Format(time.RFC3339),
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	_ = os.WriteFile(filepath.Join(targetDir, "metadata.json"), metaBytes, 0644)

	// Also write content as a markdown document inside artifacts/
	artDir := filepath.Join(targetDir, "artifacts")
	_ = os.MkdirAll(artDir, 0755)
	_ = os.WriteFile(filepath.Join(artDir, "memory.md"), []byte(item.Content), 0644)
	return nil
}
