package gitutil

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type GitStatus struct {
	IsRepo         bool      `json:"is_repo"`
	WorkDir        string    `json:"work_dir"`
	Branch         string    `json:"branch,omitempty"`
	CommitSHA      string    `json:"commit_sha,omitempty"`
	CommitMsg      string    `json:"commit_msg,omitempty"`
	CommitTime     time.Time `json:"commit_time,omitempty"`
	IsDirty        bool      `json:"is_dirty"`
	ModifiedCount  int       `json:"modified_count"`
	UntrackedCount int       `json:"untracked_count"`
	StagedCount    int       `json:"staged_count"`
	DiffStat       string    `json:"diff_stat,omitempty"`
	StatusLines    []string  `json:"status_lines,omitempty"`
}

// Inspect checks the specified directory for git status.
func Inspect(dir string) GitStatus {
	if strings.TrimSpace(dir) == "" {
		return GitStatus{}
	}
	cleanDir := strings.TrimPrefix(dir, "file://")
	cleanDir = filepath.Clean(cleanDir)

	gs := GitStatus{
		WorkDir: cleanDir,
	}

	// 1. Check if git repo
	cmd := exec.Command("git", "-C", cleanDir, "rev-parse", "--is-inside-work-tree")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil || strings.TrimSpace(out.String()) != "true" {
		gs.IsRepo = false
		return gs
	}
	gs.IsRepo = true

	// 2. Branch name
	out.Reset()
	cmd = exec.Command("git", "-C", cleanDir, "branch", "--show-current")
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil {
		gs.Branch = strings.TrimSpace(out.String())
	}
	if gs.Branch == "" {
		// Detached HEAD fallback
		out.Reset()
		cmd = exec.Command("git", "-C", cleanDir, "rev-parse", "--short", "HEAD")
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil {
			gs.Branch = "HEAD (" + strings.TrimSpace(out.String()) + ")"
		}
	}

	// 3. Head commit details
	out.Reset()
	cmd = exec.Command("git", "-C", cleanDir, "log", "-1", "--format=%H%x00%s%x00%ct")
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil {
		parts := strings.Split(strings.TrimSpace(out.String()), "\x00")
		if len(parts) >= 3 {
			gs.CommitSHA = parts[0]
			if len(gs.CommitSHA) > 10 {
				gs.CommitSHA = gs.CommitSHA[:10]
			}
			gs.CommitMsg = parts[1]
			if sec, err := strconv.ParseInt(parts[2], 10, 64); err == nil {
				gs.CommitTime = time.Unix(sec, 0)
			}
		}
	}

	// 4. Status porcelain
	out.Reset()
	cmd = exec.Command("git", "-C", cleanDir, "status", "--porcelain")
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil {
		lines := strings.Split(strings.TrimRight(out.String(), "\r\n"), "\n")
		for _, line := range lines {
			if len(line) < 2 {
				continue
			}
			gs.StatusLines = append(gs.StatusLines, line)
			x, y := line[0], line[1]
			if x == '?' && y == '?' {
				gs.UntrackedCount++
			} else {
				if x != ' ' && x != '?' {
					gs.StagedCount++
				}
				if y != ' ' && y != '?' {
					gs.ModifiedCount++
				}
			}
		}
		gs.IsDirty = (gs.ModifiedCount > 0 || gs.StagedCount > 0 || gs.UntrackedCount > 0)
	}

	// 5. Diff stat
	out.Reset()
	cmd = exec.Command("git", "-C", cleanDir, "diff", "--stat")
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil && out.Len() > 0 {
		statLines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if len(statLines) > 0 {
			gs.DiffStat = strings.TrimSpace(statLines[len(statLines)-1])
		}
	}

	return gs
}
