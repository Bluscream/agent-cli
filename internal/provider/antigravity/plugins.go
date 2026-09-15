package antigravity

import (
	"agentcli.local/ai/internal/fsutil"
	"agentcli.local/ai/internal/provider"
	"encoding/json"
	"os"
	"path/filepath"
)

func (p *AntigravityProvider) ListPlugins() ([]provider.PluginItem, error) {
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".gemini/config/plugins")
	var list []provider.PluginItem

	entries, err := os.ReadDir(pluginsDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			pDir := filepath.Join(pluginsDir, e.Name())
			pJSON := filepath.Join(pDir, "plugin.json")
			desc := "Antigravity plugin bundle (skills, rules, MCPs)"
			if data, err := os.ReadFile(pJSON); err == nil {
				var doc struct {
					Description string `json:"description"`
				}
				if err := json.Unmarshal(data, &doc); err == nil && doc.Description != "" {
					desc = doc.Description
				}
			}
			list = append(list, provider.PluginItem{
				Provider:    p.Name(),
				Name:        e.Name(),
				Type:        "Customization Bundle",
				Path:        pDir,
				Status:      "installed",
				Description: desc,
			})
		}
	}
	return list, nil
}

func (p *AntigravityProvider) InstallPlugin(sourcePath string) error {
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".gemini/config/plugins")
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		return err
	}

	name := filepath.Base(sourcePath)
	destDir := filepath.Join(pluginsDir, name)
	fi, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fsutil.CopyDir(sourcePath, destDir)
	}
	// If a single file, create a plugin directory and put it there
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destDir, filepath.Base(sourcePath)), data, 0644)
}

func (p *AntigravityProvider) UninstallPlugin(name string) error {
	if err := fsutil.ValidateName(name); err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".gemini/config/plugins")
	return fsutil.Remove(pluginsDir, name)
}
