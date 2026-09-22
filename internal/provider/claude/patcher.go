package claude

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

// asarPath is the archive inside the AppImage that carries the injected hook.
const asarPath = "usr/lib/claude-desktop/resources/app.asar"

// candidateAppImages lists where a Claude Desktop AppImage may live, most
// specific first. Gear Lever installs under ~/AppImages; older manual installs
// used ~/.local/bin.
func candidateAppImages() []string {
	home, _ := os.UserHomeDir()
	var out []string
	if env := os.Getenv("CLAUDE_APPIMAGE"); env != "" {
		out = append(out, env)
	}
	return append(out,
		filepath.Join(home, "AppImages/claude.appimage"),
		filepath.Join(home, "AppImages/Claude.AppImage"),
		filepath.Join(home, ".local/bin/Claude_Desktop.AppImage"),
		filepath.Join(home, ".local/bin/claude.appimage"),
		filepath.Join(home, "Applications/Claude.AppImage"),
	)
}

// FindAppImage returns the first AppImage that exists. When none do it returns
// the first candidate so callers still have a path to report.
func FindAppImage() string {
	candidates := candidateAppImages()
	for _, path := range candidates {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			return path
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

// containsFlag streams through r looking for needle, keeping an overlap between
// chunks so a match spanning a boundary is not missed. The asar is ~60MB, so it
// is deliberately not read into memory all at once.
func containsFlag(r io.Reader, needle []byte) bool {
	const chunk = 1 << 20
	buf := make([]byte, 0, chunk+len(needle))
	tmp := make([]byte, chunk)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if bytes.Contains(buf, needle) {
				return true
			}
			// Keep only enough tail to catch a straddling match.
			if len(buf) > len(needle)-1 {
				copy(buf, buf[len(buf)-(len(needle)-1):])
				buf = buf[:len(needle)-1]
			}
		}
		if err != nil {
			return false
		}
	}
}

// isAppImagePatched extracts just the asar from the AppImage and looks for the
// hook flag inside it.
//
// The hook lives inside app.asar inside a compressed squashfs, so running
// `strings` over the AppImage itself can never find it — that reports every
// patched image as unpatched. Extracting the single member is the cheapest
// thing that is actually correct.
func isAppImagePatched(appImage string) bool {
	tmpDir, err := os.MkdirTemp("", "claude-patch-check-")
	if err != nil {
		return false
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.Command(appImage, "--appimage-extract", asarPath)
	cmd.Dir = tmpDir
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return false
	}

	f, err := os.Open(filepath.Join(tmpDir, "squashfs-root", asarPath))
	if err != nil {
		return false
	}
	defer f.Close()

	return containsFlag(f, []byte(HookFlag))
}

func CheckPatchStatus() PatchStatus {
	home, _ := os.UserHomeDir()
	appImage := FindAppImage()
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

	if status.Exists {
		status.IsPatched = isAppImagePatched(appImage)
	}

	return status
}

// patchScript returns the patcher to invoke. The local copy is a thin launcher
// that fetches the current script from the plugin-system repo.
func patchScript() string {
	if env := os.Getenv("CLAUDE_PATCH_SCRIPT"); env != "" {
		return env
	}
	return "/run/media/system/Data/Scripts/patch-claude-desktop.sh"
}

func RunPatcher(appImagePath string) (string, error) {
	script := patchScript()
	if _, err := os.Stat(script); err != nil {
		return "", fmt.Errorf("patch script not found at %s: %w", script, err)
	}

	if appImagePath == "" {
		appImagePath = FindAppImage()
	}
	if _, err := os.Stat(appImagePath); err != nil {
		return "", fmt.Errorf("claude desktop appimage not found at %s: %w", appImagePath, err)
	}

	cmd := exec.Command(script, appImagePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("patch failed: %w (output: %s)", err, string(out))
	}
	return string(out), nil
}

func RestoreBackup() error {
	appImage := FindAppImage()
	backup := appImage + ".bak"

	if _, err := os.Stat(backup); err != nil {
		return fmt.Errorf("backup file not found at %s", backup)
	}

	// Stop running Claude
	_ = exec.Command("pkill", "-f", "claude-desktop").Run()
	_ = exec.Command("pkill", "-f", "Claude_Desktop").Run()

	return os.Rename(backup, appImage)
}
