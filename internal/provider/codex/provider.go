package codex

import (
	"agentcli.local/ai/internal/provider"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CodexProvider struct{}

func New() *CodexProvider {
	return &CodexProvider{}
}

func init() {
	provider.Register(New())
}

func (p *CodexProvider) Name() string {
	return "codex"
}

func (p *CodexProvider) DisplayName() string {
	return "Codex Desktop"
}

func (p *CodexProvider) Status() (provider.ProviderInfo, error) {
	home, _ := os.UserHomeDir()
	appImage := filepath.Join(home, "Applications/codex-desktop.AppImage")
	configPath := filepath.Join(home, ".codex/.codex-global-state.json")
	dataPath := filepath.Join(home, ".codex")

	installed := false
	if _, err := os.Stat(appImage); err == nil {
		installed = true
	}

	metrics := provider.FindProcesses("codex-desktop", "ChatGPT")

	info := provider.ProviderInfo{
		ID:             p.Name(),
		DisplayName:    p.DisplayName(),
		Origin:         "ilysenko/codex-desktop-linux (OpenAI deb repackager)",
		Installed:      installed,
		BinaryPath:     appImage,
		ConfigPath:     configPath,
		DataPath:       dataPath,
		Running:        metrics.Running,
		PIDs:           metrics.PIDs,
		CPUPercent:     metrics.CPUPercent,
		MemoryRSSBytes: metrics.MemoryRSS,
	}

	// Count MCP servers in plugins
	pluginsDir := filepath.Join(dataPath, "plugins")
	_ = filepath.Walk(pluginsDir, func(path string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && (strings.HasSuffix(path, ".mcp.json") || strings.HasSuffix(path, "desktop-mcp.json")) {
			info.MCPCount++
		}
		return nil
	})

	// Check busy: lockfiles in ~/.codex/tmp/ or recent writes to rollout files
	lockPattern := filepath.Join(dataPath, "tmp/arg0/*/.lock")
	if matches, err := filepath.Glob(lockPattern); err == nil && len(matches) > 0 {
		for _, m := range matches {
			if provider.IsFileRecentlyActive(m, 30*time.Second) {
				info.Busy = true
				info.ActiveTask = "Active agent execution lock"
				break
			}
		}
	}

	if !info.Busy {
		sessionsDir := filepath.Join(dataPath, "sessions")
		_ = filepath.Walk(sessionsDir, func(path string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() && strings.HasSuffix(path, ".jsonl") {
				if provider.IsFileRecentlyActive(path, 20*time.Second) {
					info.Busy = true
					info.ActiveTask = fmt.Sprintf("Writing rollout in %s", filepath.Base(path))
					return filepath.SkipAll
				}
			}
			return nil
		})
	}

	return info, nil
}
