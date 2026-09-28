package sqlite

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// requireSQLite skips when the binary is genuinely absent, so the suite stays
// runnable on a host without it rather than reporting a false failure.
func requireSQLite(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath(Binary()); err != nil {
		t.Skipf("sqlite3 not available: %v", err)
	}
}

// newDB builds a throwaway database under t.TempDir. Every test in this file
// writes only there; none of them touch a real provider database.
func newDB(t *testing.T, stmt string) string {
	t.Helper()
	requireSQLite(t)
	path := filepath.Join(t.TempDir(), "test.db")
	if err := Exec(path, stmt); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return path
}

func TestBindPlaceholderCountMustMatch(t *testing.T) {
	if _, err := Bind("SELECT ?, ?", "one"); err == nil {
		t.Fatal("expected an error when a placeholder has no argument")
	}
	if _, err := Bind("SELECT ?", "one", "two"); err == nil {
		t.Fatal("expected an error when an argument has no placeholder")
	}
}

func TestBindIgnoresPlaceholdersInsideStringLiterals(t *testing.T) {
	got, err := Bind("SELECT 'literal ?' , ?", "v")
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if want := "SELECT 'literal ?' , " + Literal("v"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A value carrying quotes is what the hand-rolled ”-doubling this replaced got
// wrong; it must survive a round trip unchanged.
func TestBoundValuesSurviveQuotesAndNewlines(t *testing.T) {
	path := newDB(t, "CREATE TABLE kv(k TEXT PRIMARY KEY, v TEXT);")
	value := "it's \"quoted\"\nand multi-line\tand has a ? in it"

	if err := Exec(path, "INSERT INTO kv VALUES(?, ?);", "key'name", value); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var rows []struct {
		V string `json:"v"`
	}
	if err := Query(path, "SELECT v FROM kv WHERE k = ?;", &rows, "key'name"); err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].V != value {
		t.Fatalf("round trip changed the value:\n got %q\nwant %q", rows[0].V, value)
	}
}

func TestQueryReturnsNoRowsWithoutError(t *testing.T) {
	path := newDB(t, "CREATE TABLE kv(k TEXT, v TEXT);")
	var rows []struct {
		V string `json:"v"`
	}
	if err := Query(path, "SELECT v FROM kv;", &rows); err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0", len(rows))
	}
}

// The defect that motivated this package: a failed write reported success.
func TestExecReportsFailureInsteadOfSucceedingSilently(t *testing.T) {
	requireSQLite(t)
	path := filepath.Join(t.TempDir(), "missing", "nope.db")
	if err := Exec(path, "CREATE TABLE t(a);"); err == nil {
		t.Fatal("expected an error writing to an unopenable path")
	}
}

func TestQueryCannotWrite(t *testing.T) {
	path := newDB(t, "CREATE TABLE kv(k TEXT);")
	var rows []struct{}
	if err := Query(path, "INSERT INTO kv VALUES('x');", &rows); err == nil {
		t.Fatal("expected -readonly to reject a write issued through Query")
	}
}

func TestScalarReturnsOneEntryPerRow(t *testing.T) {
	path := newDB(t, "CREATE TABLE kv(v TEXT); INSERT INTO kv VALUES('a'),('b');")
	got, err := Scalar(path, "SELECT v FROM kv ORDER BY v;")
	if err != nil {
		t.Fatalf("scalar: %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %q, want [a b]", got)
	}
}

func TestBackupProducesAReadableCopy(t *testing.T) {
	path := newDB(t, "CREATE TABLE kv(v TEXT); INSERT INTO kv VALUES('kept');")
	dest := filepath.Join(t.TempDir(), "copy.db")
	if err := Backup(path, dest); err != nil {
		t.Fatalf("backup: %v", err)
	}
	var rows []struct {
		V string `json:"v"`
	}
	if err := Query(dest, "SELECT v FROM kv;", &rows); err != nil {
		t.Fatalf("query copy: %v", err)
	}
	if len(rows) != 1 || rows[0].V != "kept" {
		t.Fatalf("backup did not carry the row: %+v", rows)
	}
}

func TestBackupRejectsAQuotedDestination(t *testing.T) {
	path := newDB(t, "CREATE TABLE kv(v TEXT);")
	if err := Backup(path, filepath.Join(t.TempDir(), "it's.db")); err == nil {
		t.Fatal("expected a destination containing a quote to be rejected")
	}
}
