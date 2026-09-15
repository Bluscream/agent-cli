package provider

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ProcessMetrics struct {
	PIDs       []int
	MemoryRSS  int64
	CPUPercent float64
	Running    bool
}

// FindProcesses scans /proc for processes whose cmdline or comm matches any of the patterns.
func FindProcesses(patterns ...string) ProcessMetrics {
	var metrics ProcessMetrics
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return metrics
	}

	seen := make(map[int]bool)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		cmdlinePath := filepath.Join("/proc", entry.Name(), "cmdline")
		cmdlineBytes, err := os.ReadFile(cmdlinePath)
		if err != nil {
			continue
		}
		cmdline := strings.ToLower(string(cmdlineBytes))

		commPath := filepath.Join("/proc", entry.Name(), "comm")
		commBytes, _ := os.ReadFile(commPath)
		comm := strings.ToLower(string(commBytes))

		matched := false
		for _, pat := range patterns {
			p := strings.ToLower(pat)
			if strings.Contains(cmdline, p) || strings.Contains(comm, p) {
				matched = true
				break
			}
		}

		if matched && !seen[pid] {
			seen[pid] = true
			metrics.PIDs = append(metrics.PIDs, pid)

			// Read RSS memory from /proc/[pid]/status
			rss := readProcessRSS(pid)
			metrics.MemoryRSS += rss
		}
	}

	if len(metrics.PIDs) > 0 {
		metrics.Running = true
		// Approximate CPU % from top primary PID
		metrics.CPUPercent = sampleProcessCPU(metrics.PIDs[0])
	}

	return metrics
}

func readProcessRSS(pid int) int64 {
	statusFile := fmt.Sprintf("/proc/%d/status", pid)
	f, err := os.Open(statusFile)
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, err := strconv.ParseInt(fields[1], 10, 64)
				if err == nil {
					return kb * 1024
				}
			}
		}
	}
	return 0
}

func sampleProcessCPU(pid int) float64 {
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	readTicks := func() (int64, int64) {
		data, err := os.ReadFile(statPath)
		if err != nil {
			return 0, 0
		}
		fields := strings.Fields(string(data))
		if len(fields) < 15 {
			return 0, 0
		}
		utime, _ := strconv.ParseInt(fields[13], 10, 64)
		stime, _ := strconv.ParseInt(fields[14], 10, 64)
		return utime, stime
	}

	u1, s1 := readTicks()
	if u1 == 0 && s1 == 0 {
		return 0.0
	}
	time.Sleep(35 * time.Millisecond)
	u2, s2 := readTicks()
	diff := (u2 - u1) + (s2 - s1)
	if diff < 0 {
		diff = 0
	}
	pct := float64(diff) / 3.5
	if pct > 100.0 {
		pct = 100.0
	}
	return pct
}

// IsFileRecentlyActive checks if a path was modified within maxAge.
func IsFileRecentlyActive(path string, maxAge time.Duration) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(fi.ModTime()) < maxAge
}
