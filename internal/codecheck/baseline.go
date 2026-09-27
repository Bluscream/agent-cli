package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// BaselineFile lists the oversized functions that predate this check, each with
// the size it had when it was recorded. It exists so the limits can be enforced
// from here on without a sweeping refactor of code nobody is touching.
//
// The list may only ever shrink. A function not listed fails. A listed function
// that grew past its recorded size fails, so an allowance is never a licence to
// keep adding. A listed function that now fits fails as stale, which forces the
// entry to be deleted once the code is split.
const BaselineFile = ".codecheck-baseline"

// Baseline maps a violation identity to the largest size it is allowed to have.
// A file with two oversized literals shares one identity, so the second one is
// reported rather than silently excused.
type Baseline struct {
	allowed map[string]int
	path    string
}

// LoadBaseline reads the baseline. A missing file is an empty baseline, so every
// violation is reported.
func LoadBaseline(path string) (*Baseline, error) {
	baseline := &Baseline{allowed: map[string]int{}, path: path}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return baseline, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Split at the last space: the symbol itself may contain one, as
		// "function literal" does.
		split := strings.LastIndex(line, " ")
		if split < 0 {
			return nil, fmt.Errorf("%s:%d: expected \"<path>:<symbol> <lines>\", got %q", path, lineNo, line)
		}
		key, size := line[:split], line[split+1:]
		lines, convErr := strconv.Atoi(strings.TrimSpace(size))
		if convErr != nil {
			return nil, fmt.Errorf("%s:%d: %q is not a line count: %w", path, lineNo, size, convErr)
		}
		if _, duplicate := baseline.allowed[key]; duplicate {
			return nil, fmt.Errorf("%s:%d: %q is listed twice", path, lineNo, key)
		}
		baseline.allowed[key] = lines
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return baseline, nil
}

// Report is the outcome of checking violations against a baseline.
type Report struct {
	New   []Violation // unlisted, or grown past the recorded size
	Grown []Violation // subset of New that is listed but larger than recorded
	Stale []string    // listed identities that no longer breach a limit
}

// OK reports whether the check passes.
func (r Report) OK() bool { return len(r.New) == 0 && len(r.Stale) == 0 }

// Check classifies violations against the baseline.
func (b *Baseline) Check(violations []Violation) Report {
	unseen := make(map[string]struct{}, len(b.allowed))
	for key := range b.allowed {
		unseen[key] = struct{}{}
	}

	var report Report
	matched := map[string]bool{}

	for _, violation := range violations {
		key := violation.Key()
		allowance, listed := b.allowed[key]
		delete(unseen, key)

		switch {
		case !listed, matched[key]:
			// Unlisted, or a second violation sharing one identity.
			report.New = append(report.New, violation)
		case violation.Lines > allowance:
			report.New = append(report.New, violation)
			report.Grown = append(report.Grown, violation)
			matched[key] = true
		default:
			matched[key] = true
		}
	}

	for key := range unseen {
		report.Stale = append(report.Stale, key)
	}
	sort.Strings(report.Stale)
	return report
}

// Allowance returns the recorded size for an identity, for reporting growth.
func (b *Baseline) Allowance(key string) int { return b.allowed[key] }
