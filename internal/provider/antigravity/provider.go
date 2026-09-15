package antigravity

import (
	"agentcli.local/ai/internal/provider"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type AntigravityProvider struct{}

func New() *AntigravityProvider {
	return &AntigravityProvider{}
}

func init() {
	provider.Register(New())
}

func (p *AntigravityProvider) Name() string {
	return "antigravity"
}

func (p *AntigravityProvider) DisplayName() string {
	return "Antigravity IDE"
}

func (p *AntigravityProvider) Status() (provider.ProviderInfo, error) {
	home, _ := os.UserHomeDir()
	binPath := "/var/home/linuxbrew/.linuxbrew/bin/antigravity-ide"
	configPath := filepath.Join(home, ".config/Antigravity IDE/User/globalStorage/state.vscdb")
	dataPath := filepath.Join(home, ".gemini/antigravity-ide")

	installed := false
	if _, err := os.Stat(binPath); err == nil {
		installed = true
	} else if _, err := exec.LookPath("antigravity-ide"); err == nil {
		installed = true
		binPath, _ = exec.LookPath("antigravity-ide")
	}

	metrics := provider.FindProcesses("antigravity-ide")

	info := provider.ProviderInfo{
		ID:             p.Name(),
		DisplayName:    p.DisplayName(),
		Origin:         "ublue-os/tap/antigravity-ide-linux (Homebrew)",
		Installed:      installed,
		BinaryPath:     binPath,
		ConfigPath:     configPath,
		DataPath:       dataPath,
		Running:        metrics.Running,
		PIDs:           metrics.PIDs,
		CPUPercent:     metrics.CPUPercent,
		MemoryRSSBytes: metrics.MemoryRSS,
	}

	// Count MCP servers configured in antigravity
	mcpPath := filepath.Join(dataPath, "mcp_config.json")
	if data, err := os.ReadFile(mcpPath); err == nil {
		var mcpDoc struct {
			MCPServers map[string]any `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &mcpDoc); err == nil {
			info.MCPCount = len(mcpDoc.MCPServers)
		}
	}

	// Determine if busy (activity in any brain transcript or running task log within 15s)
	brainDir := filepath.Join(dataPath, "brain")
	if entries, err := os.ReadDir(brainDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				tr := filepath.Join(brainDir, e.Name(), ".system_generated", "logs", "transcript.jsonl")
				if provider.IsFileRecentlyActive(tr, 20*time.Second) {
					info.Busy = true
					info.ActiveTask = fmt.Sprintf("Active turn in conversation %.8s", e.Name())
					break
				}
			}
		}
	}

	return info, nil
}
