// Command codecheck enforces the repository's physical-line limits: 1000 lines
// per code file and 100 lines per function, including function literals.
//
// Functions listed in .codecheck-baseline are excused, so the limits can be
// enforced from here on without one sweeping refactor of code nobody is
// touching. That list may only shrink: an unlisted breach fails, and so does a
// listed entry that no longer breaches anything.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func main() {
	if err := run("."); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(root string) error {
	violations, notices, err := Scan(root)
	if err != nil {
		return err
	}
	// Resolve the baseline against the scanned tree, not the process's working
	// directory, so violation paths and baseline keys share one root.
	baseline, err := LoadBaseline(filepath.Join(root, BaselineFile))
	if err != nil {
		return err
	}

	for _, notice := range notices {
		fmt.Println(notice)
	}

	report := baseline.Check(violations)
	if report.OK() {
		fmt.Printf("Size limits satisfied: %d baselined oversized function(s), none new.\n", len(violations))
		return nil
	}

	grown := map[string]bool{}
	for _, violation := range report.Grown {
		grown[violation.Key()] = true
	}

	if len(report.New) > 0 {
		sort.Slice(report.New, func(i, j int) bool {
			if report.New[i].Path != report.New[j].Path {
				return report.New[i].Path < report.New[j].Path
			}
			return report.New[i].Line < report.New[j].Line
		})
		fmt.Printf("\n%d size-limit violation(s) not covered by the baseline:\n", len(report.New))
		for _, violation := range report.New {
			if grown[violation.Key()] {
				fmt.Printf("  %s (baselined at %d lines — it grew; split it rather than raising the entry)\n",
					violation, baseline.Allowance(violation.Key()))
				continue
			}
			fmt.Printf("  %s\n", violation)
		}
		fmt.Printf("\nSplit them. Only add an entry to %s with a reason it cannot be split now.\n", BaselineFile)
	}

	if len(report.Stale) > 0 {
		fmt.Printf("\n%d stale %s entry/entries — that code is within limits now, so delete these lines:\n",
			len(report.Stale), BaselineFile)
		for _, key := range report.Stale {
			fmt.Printf("  %s\n", key)
		}
	}
	return fmt.Errorf("size limits not satisfied")
}
