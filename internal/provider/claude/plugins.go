package claude

import (
	"agentcli.local/ai/internal/fsutil"
	"agentcli.local/ai/internal/provider"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (p *ClaudeProvider) ListPlugins() ([]provider.PluginItem, error) {
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".config/Claude/plugins")
	var list []provider.PluginItem

	// Check loader hook status
	pst := CheckPatchStatus()
	hookStatus := "unpatched"
	if pst.IsPatched {
		hookStatus = "active"
	}
	list = append(list, provider.PluginItem{
		Provider:    p.Name(),
		Name:        "claude-asar-hook",
		Type:        "ASAR Hook",
		Path:        pst.AppImagePath,
		Status:      hookStatus,
		Description: "Electron main-process injector hook for Claude Desktop AppImage",
	})

	entries, err := os.ReadDir(pluginsDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			// The plugins directory also holds per-plugin config files; only
			// .js files are ever loaded.
			if !strings.HasSuffix(name, ".js") {
				continue
			}

			status := "installed"
			if name == "loader.js" {
				status = "loader"
			}

			// The loader requires *.main.js in the Electron main process and
			// injects everything else into the renderer via executeJavaScript.
			kind := "Web Script / Plugin"
			where := "injected into Claude Desktop WebContents"
			if strings.HasSuffix(name, ".main.js") {
				kind = "Main Process Plugin"
				where = "loaded in the Electron main process"
			}

			list = append(list, provider.PluginItem{
				Provider:    p.Name(),
				Name:        name,
				Type:        kind,
				Path:        filepath.Join(pluginsDir, name),
				Status:      status,
				Description: fmt.Sprintf("JavaScript plugin %s (%s)", where, name),
			})
		}
	}
	return list, nil
}

func (p *ClaudeProvider) InstallPlugin(sourcePath string) error {
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".config/Claude/plugins")
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		return err
	}

	fi, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	destPath := filepath.Join(pluginsDir, filepath.Base(sourcePath))
	if fi.IsDir() {
		return fsutil.CopyDir(sourcePath, destPath)
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	return os.WriteFile(destPath, data, 0644)
}

func (p *ClaudeProvider) UninstallPlugin(name string) error {
	if err := fsutil.ValidateName(name); err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".config/Claude/plugins")
	target := filepath.Join(pluginsDir, name)
	if _, err := os.Stat(target); err != nil {
		// try with .js
		target = filepath.Join(pluginsDir, name+".js")
	}
	return fsutil.Remove(pluginsDir, filepath.Base(target))
}
