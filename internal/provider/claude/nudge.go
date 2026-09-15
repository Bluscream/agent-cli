package claude

import (
	"fmt"
	"os"
	"os/exec"
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

const MacroScript = "/run/media/system/Data/Scripts/claude_nudge_macro.py"

func CheckNudgeStatus() NudgeStatus {
	status := NudgeStatus{
		ScriptPath: MacroScript,
	}
	if _, err := os.Stat(MacroScript); err == nil {
		status.Exists = true
	}

	metrics := provider.FindProcesses("claude_nudge_macro.py")
	status.Running = metrics.Running
	status.PIDs = metrics.PIDs
	status.CPUPercent = metrics.CPUPercent
	return status
}

func StartNudgeMacro() error {
	if _, err := os.Stat(MacroScript); err != nil {
		return fmt.Errorf("nudge script not found at %s", MacroScript)
	}

	cmd := exec.Command("python3", MacroScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

func StopNudgeMacro() error {
	return exec.Command("pkill", "-f", "claude_nudge_macro.py").Run()
}
