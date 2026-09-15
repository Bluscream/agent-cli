package claude

import (
	"agentcli.local/ai/internal/provider"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ClaudeProvider struct{}

func New() *ClaudeProvider {
	return &ClaudeProvider{}
}

func init() {
	provider.Register(New())
}

func (p *ClaudeProvider) Name() string {
	return "claude"
}

func (p *ClaudeProvider) DisplayName() string {
	return "Claude Desktop"
}

func (p *ClaudeProvider) Status() (provider.ProviderInfo, error) {
	home, _ := os.UserHomeDir()
	appImage := filepath.Join(home, ".local/bin/Claude_Desktop.AppImage")
	configPath := filepath.Join(home, ".config/Claude/claude_desktop_config.json")
	dataPath := filepath.Join(home, ".config/Claude")

	installed := false
	if _, err := os.Stat(appImage); err == nil {
		installed = true
	}

	metrics := provider.FindProcesses("claude_desktop", "claude-desktop")

	info := provider.ProviderInfo{
		ID:             p.Name(),
		DisplayName:    p.DisplayName(),
		Origin:         "io.github.aaddrick.claude-desktop-debian (GitHub)",
		Installed:      installed,
		BinaryPath:     appImage,
		ConfigPath:     configPath,
		DataPath:       dataPath,
		Running:        metrics.Running,
		PIDs:           metrics.PIDs,
		CPUPercent:     metrics.CPUPercent,
		MemoryRSSBytes: metrics.MemoryRSS,
	}

	// Count MCP servers
	if data, err := os.ReadFile(configPath); err == nil {
		var doc struct {
			MCPServers map[string]any `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &doc); err == nil {
			info.MCPCount = len(doc.MCPServers)
		}
	}

	// Determine if busy (activity in any claude project transcript within 20s)
	projectsDir := filepath.Join(home, ".claude/projects")
	_ = filepath.Walk(projectsDir, func(path string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.HasSuffix(path, ".jsonl") {
			if provider.IsFileRecentlyActive(path, 25*time.Second) {
				info.Busy = true
				info.ActiveTask = fmt.Sprintf("Active transcript in %s", filepath.Base(path))
				return filepath.SkipAll
			}
		}
		return nil
	})

	return info, nil
}
