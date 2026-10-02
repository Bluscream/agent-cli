package claude

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"agentcli.local/ai/internal/provider"
)

type NudgeStatus struct {
	ScriptPath string  `json:"script_path"`
	Exists     bool    `json:"exists"`
	Running    bool    `json:"running"`
	PIDs       []int   `json:"pids,omitempty"`
	CPUPercent float64 `json:"cpu_percent,omitempty"`
}

// EnvMacroScript overrides where the nudge macro is looked for.
const EnvMacroScript = "CLAUDE_NUDGE_MACRO"

// macroScriptName is the file the macro is installed as.
const macroScriptName = "claude_nudge_macro.py"

// MacroScript resolves the nudge macro's path: the environment first, then the
// places a user script is installed on this host. It was a single absolute path
// under one machine's project directory, so the check could only ever succeed
// there.
func MacroScript() string {
	if env := strings.TrimSpace(os.Getenv(EnvMacroScript)); env != "" {
		return env
	}
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".local/share/agent-cli", macroScriptName),
		filepath.Join(home, ".local/bin", macroScriptName),
		filepath.Join(home, "bin", macroScriptName),
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	// Nothing found: report the preferred location so the message names where
	// to put it rather than a path that was only ever a guess.
	return candidates[0]
}

func CheckNudgeStatus() NudgeStatus {
	script := MacroScript()
	status := NudgeStatus{
		ScriptPath: script,
	}
	if _, err := os.Stat(script); err == nil {
		status.Exists = true
	}

	metrics := provider.FindProcesses("claude_nudge_macro.py")
	status.Running = metrics.Running
	status.PIDs = metrics.PIDs
	status.CPUPercent = metrics.CPUPercent
	return status
}

func StartNudgeMacro() error {
	script := MacroScript()
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("nudge script not found at %s: %w", script, err)
	}

	cmd := exec.Command("python3", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

func StopNudgeMacro() error {
	return exec.Command("pkill", "-f", "claude_nudge_macro.py").Run()
}
