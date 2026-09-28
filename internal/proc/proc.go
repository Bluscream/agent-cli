// Package proc stops and starts the desktop applications whose state this tool
// edits. Both providers need the same escalation and the same detached launch,
// and when it was written twice only one copy got scoped to the current user.
package proc

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// stopPhases is the escalation: ask nicely, insist, then compel. The first
// signal is SIGINT because both applications flush their state on it, so a
// harder signal first would lose the session that is about to be copied.
var stopPhases = []string{"-INT", "-TERM", "-9"}

const (
	pollInterval = 500 * time.Millisecond
	pollAttempts = 6
)

// Stop terminates processes matching pattern that belong to the current user,
// escalating until they exit. The user scoping is not optional: on a
// multi-user host a bare `pkill -f` signals another person's editor.
//
// It reports whether the processes are gone. A caller that is about to
// overwrite the application's database should not proceed on false: a running
// application holds the lock and will rewrite what was just restored.
func Stop(pattern string) bool {
	uid := strconv.Itoa(os.Getuid())

	for _, signal := range stopPhases {
		// A pkill that matched nothing exits 1, which is the success case
		// here, so its status says nothing useful and is not checked.
		_ = exec.Command("pkill", "-u", uid, signal, "-f", pattern).Run()
		for range pollAttempts {
			time.Sleep(pollInterval)
			if !Running(pattern) {
				return true
			}
		}
	}
	// After SIGKILL the kernel still needs a moment to reap them.
	time.Sleep(pollInterval)
	return !Running(pattern)
}

// Running reports whether the current user has a process matching pattern.
func Running(pattern string) bool {
	uid := strconv.Itoa(os.Getuid())
	return exec.Command("pgrep", "-u", uid, "-f", pattern).Run() == nil
}

// Launch starts the first of candidates that exists, falling back to the given
// names on PATH, detached from this process so it outlives the CLI. env entries
// are added to the inherited environment.
func Launch(what string, candidates, pathNames []string, env ...string) error {
	path := Find(candidates, pathNames)
	if path == "" {
		return fmt.Errorf("%s executable not found", what)
	}
	cmd := exec.Command(path)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// Find resolves an executable: explicit paths first so a locally installed
// build wins, then PATH.
func Find(candidates, pathNames []string) string {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	for _, n := range pathNames {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}
