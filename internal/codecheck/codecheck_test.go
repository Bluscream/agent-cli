package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGo puts a Go file with a function of the requested body length in dir.
func writeGo(t *testing.T, dir, name, funcName string, bodyLines int) {
	t.Helper()
	var body strings.Builder
	for i := 0; i < bodyLines; i++ {
		body.WriteString("\t_ = 1\n")
	}
	source := "package sample\n\nfunc " + funcName + "() {\n" + body.String() + "}\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestScanFlagsOversizedFunctions(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "big.go", "TooLong", FuncLimit+5)
	writeGo(t, dir, "small.go", "Fine", 5)

	violations, _, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 {
		t.Fatalf("got %d violations, want 1: %+v", len(violations), violations)
	}
	got := violations[0]
	if got.Symbol != "TooLong" {
		t.Errorf("symbol = %q, want TooLong", got.Symbol)
	}
	if got.Lines <= FuncLimit {
		t.Errorf("reported %d lines, which is within the limit", got.Lines)
	}
	if got.Key() != "big.go:TooLong" {
		t.Errorf("key = %q, want a line-number-free identity", got.Key())
	}
}

func TestScanSkipsVendoredAndBuildDirs(t *testing.T) {
	dir := t.TempDir()
	for _, skipped := range []string{"bin", ".git", ".references"} {
		sub := filepath.Join(dir, skipped)
		if err := os.MkdirAll(sub, 0o700); err != nil {
			t.Fatal(err)
		}
		writeGo(t, sub, "big.go", "TooLong", FuncLimit+5)
	}

	violations, _, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Errorf("scanned skipped directories: %+v", violations)
	}
}

func TestFileWarningIsNoticeNotFailure(t *testing.T) {
	dir := t.TempDir()
	// One line per iteration, landing between the warn threshold and the limit.
	var source strings.Builder
	source.WriteString("package sample\n")
	for i := 0; i < FileWarn+10; i++ {
		source.WriteString("// padding\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "long.go"), []byte(source.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	violations, notices, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Errorf("a file under the hard limit failed: %+v", violations)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "approaching") {
		t.Errorf("no notice for a file approaching the limit: %v", notices)
	}
}

func loadBaselineFrom(t *testing.T, contents string) *Baseline {
	t.Helper()
	path := filepath.Join(t.TempDir(), "baseline")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	return baseline
}

// A baselined function is excused only at or below its recorded size.
func TestBaselineExcusesRecordedSize(t *testing.T) {
	baseline := loadBaselineFrom(t, "# comment\n\nsample.go:Known 150\n")
	violation := Violation{Path: "sample.go", Symbol: "Known", Lines: 150, Limit: FuncLimit}

	if report := baseline.Check([]Violation{violation}); !report.OK() {
		t.Errorf("a violation at its recorded size failed: %+v", report)
	}

	shrunk := violation
	shrunk.Lines = 120
	if report := baseline.Check([]Violation{shrunk}); !report.OK() {
		t.Errorf("a shrinking violation failed: %+v", report)
	}
}

// Growing past the recorded size must fail: an entry is not a licence to keep
// adding to an already-oversized function.
func TestBaselineRejectsGrowth(t *testing.T) {
	baseline := loadBaselineFrom(t, "sample.go:Known 150\n")
	grown := Violation{Path: "sample.go", Symbol: "Known", Lines: 151, Limit: FuncLimit}

	report := baseline.Check([]Violation{grown})
	if report.OK() {
		t.Fatal("a baselined function was allowed to grow")
	}
	if len(report.Grown) != 1 {
		t.Errorf("growth not reported as such: %+v", report)
	}
	if baseline.Allowance("sample.go:Known") != 150 {
		t.Errorf("allowance = %d, want 150", baseline.Allowance("sample.go:Known"))
	}
}

func TestBaselineRejectsUnlistedViolation(t *testing.T) {
	baseline := loadBaselineFrom(t, "sample.go:Known 150\n")
	report := baseline.Check([]Violation{
		{Path: "other.go", Symbol: "Fresh", Lines: 120, Limit: FuncLimit},
	})

	if report.OK() {
		t.Fatal("an unlisted violation passed")
	}
	if len(report.New) != 1 || report.New[0].Symbol != "Fresh" {
		t.Errorf("wrong violation reported: %+v", report.New)
	}
}

// A stale entry must fail, so the list shrinks as code is split rather than
// accumulating allowances nobody revisits.
func TestBaselineRejectsStaleEntry(t *testing.T) {
	baseline := loadBaselineFrom(t, "sample.go:Fixed 150\n")

	report := baseline.Check(nil)
	if report.OK() {
		t.Fatal("a stale entry passed")
	}
	if len(report.Stale) != 1 || report.Stale[0] != "sample.go:Fixed" {
		t.Errorf("stale entry not named: %+v", report.Stale)
	}
}

// Two oversized literals in one file share an identity; the second must be
// reported rather than silently covered by the single entry.
func TestBaselineDoesNotCoverASecondViolationOfOneIdentity(t *testing.T) {
	baseline := loadBaselineFrom(t, "sample.go:function literal 130\n")
	report := baseline.Check([]Violation{
		{Path: "sample.go", Symbol: "function literal", Lines: 130, Limit: FuncLimit},
		{Path: "sample.go", Symbol: "function literal", Lines: 125, Limit: FuncLimit},
	})

	if report.OK() {
		t.Fatal("a second violation sharing one identity was excused")
	}
	if len(report.New) != 1 {
		t.Errorf("got %d new violations, want 1: %+v", len(report.New), report.New)
	}
}

func TestBaselineRejectsMalformedEntries(t *testing.T) {
	for name, contents := range map[string]string{
		"no size":    "sample.go:Known\n",
		"bad size":   "sample.go:Known lots\n",
		"duplicated": "sample.go:Known 150\nsample.go:Known 160\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "baseline")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadBaseline(path); err == nil {
				t.Errorf("malformed baseline %q was accepted", contents)
			}
		})
	}
}

func TestMissingBaselineReportsEverything(t *testing.T) {
	baseline, err := LoadBaseline(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("a missing baseline is not an error: %v", err)
	}
	report := baseline.Check([]Violation{{Path: "a.go", Symbol: "F", Lines: 120, Limit: FuncLimit}})
	if report.OK() {
		t.Error("an empty baseline excused a violation")
	}
}

// repoRoot walks up from the working directory to the module root. The gate
// runs compiled test binaries from a staging directory, so a fixed relative
// path such as "../.." can land outside the repository entirely.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Skipf("cannot determine the working directory: %v", err)
	}

	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			if strings.Contains(string(data), "module agentcli.local/ai") {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("not running inside the repository; the build gate covers this")
		}
		dir = parent
	}
}

// The repository's own baseline must stay valid and honest.
func TestRepositoryBaselineIsCurrent(t *testing.T) {
	if err := run(repoRoot(t)); err != nil {
		t.Fatalf("the repository does not satisfy its own size baseline: %v", err)
	}
}
