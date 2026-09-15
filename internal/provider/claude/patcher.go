package claude

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type PatchStatus struct {
	AppImagePath string `json:"app_image_path"`
	Exists       bool   `json:"exists"`
	HasBackup    bool   `json:"has_backup"`
	BackupPath   string `json:"backup_path,omitempty"`
	PluginsDir   string `json:"plugins_dir"`
	LoaderExists bool   `json:"loader_exists"`
	HookFlag     string `json:"hook_flag"`
	IsPatched    bool   `json:"is_patched"`
}

const HookFlag = "/* CLAUDE_PLUGIN_LOADER_HOOK */"

func CheckPatchStatus() PatchStatus {
	home, _ := os.UserHomeDir()
	appImage := filepath.Join(home, ".local/bin/Claude_Desktop.AppImage")
	backup := appImage + ".bak"
	pluginsDir := filepath.Join(home, ".config/Claude/plugins")
	loaderPath := filepath.Join(pluginsDir, "loader.js")

	status := PatchStatus{
		AppImagePath: appImage,
		BackupPath:   backup,
		PluginsDir:   pluginsDir,
		HookFlag:     HookFlag,
	}

	if _, err := os.Stat(appImage); err == nil {
		status.Exists = true
	}
	if _, err := os.Stat(backup); err == nil {
		status.HasBackup = true
	}
	if _, err := os.Stat(loaderPath); err == nil {
		status.LoaderExists = true
	}

	// Check if hook is installed inside AppImage without full extraction if possible
	if status.Exists {
		cmd := exec.Command("strings", appImage)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil {
			if strings.Contains(out.String(), "CLAUDE_PLUGIN_LOADER_HOOK") {
				status.IsPatched = true
			}
		}
	}

	return status
}

func RunPatcher(appImagePath string) (string, error) {
	patchScript := "/run/media/system/Data/Scripts/patch-claude-desktop.sh"
	if _, err := os.Stat(patchScript); err != nil {
		return "", fmt.Errorf("patch script not found at %s: %w", patchScript, err)
	}

	if appImagePath == "" {
		home, _ := os.UserHomeDir()
		appImagePath = filepath.Join(home, ".local/bin/Claude_Desktop.AppImage")
	}

	cmd := exec.Command(patchScript, appImagePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("patch failed: %w (output: %s)", err, string(out))
	}
	return string(out), nil
}

func RestoreBackup() error {
	home, _ := os.UserHomeDir()
	appImage := filepath.Join(home, ".local/bin/Claude_Desktop.AppImage")
	backup := appImage + ".bak"

	if _, err := os.Stat(backup); err != nil {
		return fmt.Errorf("backup file not found at %s", backup)
	}

	// Stop running Claude
	_ = exec.Command("pkill", "-f", "claude-desktop").Run()
	_ = exec.Command("pkill", "-f", "Claude_Desktop").Run()

	return os.Rename(backup, appImage)
}
