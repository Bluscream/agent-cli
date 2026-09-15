package cli

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
)

type StepTiming struct {
	Name     string        `json:"step"`
	Duration string        `json:"duration"`
	Nanos    int64         `json:"nanos"`
	Elapsed  time.Duration `json:"-"`
}

type DebugMetrics struct {
	BuildMode     string       `json:"build_mode"`
	TotalDuration string       `json:"total_duration"`
	TotalNanos    int64        `json:"total_nanos"`
	Steps         []StepTiming `json:"steps"`
}

type Timer struct {
	mu        sync.Mutex
	start     time.Time
	lastStep  time.Time
	steps     []StepTiming
	enabled   bool
	buildMode string
}

func NewTimer(explicitDebug bool) *Timer {
	enabled := IsDebugBuild || explicitDebug || os.Getenv("AI_DEBUG") == "1" || os.Getenv("DEBUG") == "1"
	mode := "release"
	if IsDebugBuild {
		mode = "debug"
	}
	now := time.Now()
	return &Timer{
		start:     now,
		lastStep:  now,
		enabled:   enabled,
		buildMode: mode,
	}
}

func (t *Timer) IsEnabled() bool {
	if t == nil {
		return false
	}
	return t.enabled
}

func (t *Timer) Step(name string) {
	if t == nil || !t.enabled {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	dur := now.Sub(t.lastStep)
	t.lastStep = now

	t.steps = append(t.steps, StepTiming{
		Name:     name,
		Duration: dur.String(),
		Nanos:    dur.Nanoseconds(),
		Elapsed:  dur,
	})
}

func (t *Timer) Metrics() DebugMetrics {
	if t == nil {
		return DebugMetrics{BuildMode: "unknown"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	total := time.Since(t.start)
	return DebugMetrics{
		BuildMode:     t.buildMode,
		TotalDuration: total.String(),
		TotalNanos:    total.Nanoseconds(),
		Steps:         t.steps,
	}
}

func (t *Timer) RenderFooter(w io.Writer, o *options) {
	if t == nil || !t.enabled || o.isJSON() {
		return
	}
	m := t.Metrics()
	if len(m.Steps) == 0 {
		return
	}

	tbl := o.newTable(w)
	tbl.SetTitle(fmt.Sprintf("DEBUG TIMINGS [mode: %s, total: %s]", m.BuildMode, m.TotalDuration))
	tbl.AppendHeader(table.Row{"Step / Operation", "Duration"})

	for _, s := range m.Steps {
		tbl.AppendRow(table.Row{s.Name, s.Duration})
	}
	fmt.Fprintln(w, o.renderTable(tbl))
}
