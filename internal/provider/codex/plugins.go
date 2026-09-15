package codex

import (
	"agentcli.local/ai/internal/fsutil"
	"agentcli.local/ai/internal/provider"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (p *CodexProvider) ListPlugins() ([]provider.PluginItem, error) {
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".codex/plugins")
	var list []provider.PluginItem

	// 1. Check cached / installed plugins
	cacheDir := filepath.Join(pluginsDir, "cache")
	_ = filepath.Walk(cacheDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil || !fi.IsDir() {
			return nil
		}
		// If directory contains .codex-plugin, it's a plugin bundle
		manifest := filepath.Join(path, ".codex-plugin")
		if _, err := os.Stat(manifest); err == nil {
			rel, _ := filepath.Rel(cacheDir, path)
			parts := strings.Split(rel, string(os.PathSeparator))
			name := filepath.Base(path)
			if len(parts) >= 2 {
				name = parts[len(parts)-2] // plugin directory name before version
			}
			list = append(list, provider.PluginItem{
				Provider:    p.Name(),
				Name:        name,
				Type:        "Codex Plugin Bundle",
				Path:        path,
				Status:      "active",
				Description: fmt.Sprintf("Codex plugin extension (%s)", rel),
			})
		}
		return nil
	})

	// 2. Check plugin appserver host
	appserverDir := filepath.Join(pluginsDir, ".plugin-appserver")
	if entries, err := os.ReadDir(appserverDir); err == nil {
		for _, e := range entries {
			list = append(list, provider.PluginItem{
				Provider:    p.Name(),
				Name:        e.Name(),
				Type:        "App Server Host",
				Path:        filepath.Join(appserverDir, e.Name()),
				Status:      "installed",
				Description: fmt.Sprintf("Codex plugin host runtime (%s)", e.Name()),
			})
		}
	}

	return list, nil
}

func (p *CodexProvider) InstallPlugin(sourcePath string) error {
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".codex/plugins")
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
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destDir, filepath.Base(sourcePath)), data, 0644)
}

func (p *CodexProvider) UninstallPlugin(name string) error {
	if err := fsutil.ValidateName(name); err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".codex/plugins")
	// Look in plugins root, cache, or appserver
	targets := []string{
		filepath.Join(pluginsDir, name),
		filepath.Join(pluginsDir, "cache", "openai-bundled", name),
		filepath.Join(pluginsDir, "cache", "openai-curated-remote", name),
	}
	for _, t := range targets {
		if _, err := os.Stat(t); err == nil {
			return fsutil.Remove(filepath.Dir(t), filepath.Base(t))
		}
	}
	return nil
}
