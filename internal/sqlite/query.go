package sqlite

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// Query runs a read-only statement and decodes its rows into dest, which must
// be a pointer to a slice of structs with `json` tags naming the columns.
//
// The database is opened -readonly, so a Query can never mutate it even if the
// statement says otherwise.
func Query(path, stmt string, dest any, args ...string) error {
	bound, err := Bind(stmt, args...)
	if err != nil {
		return err
	}
	out, err := run(path, []string{"-readonly", "-json", "-bail"}, bound)
	if err != nil {
		return err
	}
	data := strings.TrimSpace(out)
	if data == "" {
		// sqlite3 prints nothing at all for an empty result set.
		data = "[]"
	}
	if err := json.Unmarshal([]byte(data), dest); err != nil {
		return fmt.Errorf("decode sqlite rows: %w", err)
	}
	return nil
}

// Scalar runs a read-only statement expected to yield a single column and
// returns the raw text of each row, in order. For a single value, take the
// first element after checking the length.
func Scalar(path, stmt string, args ...string) ([]string, error) {
	bound, err := Bind(stmt, args...)
	if err != nil {
		return nil, err
	}
	out, err := run(path, []string{"-readonly", "-noheader", "-bail"}, bound)
	if err != nil {
		return nil, err
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// Text runs statements that may write and returns their trimmed stdout. It
// exists for statements whose output is itself SQL — the Claude cookie extract
// builds a replayable script — where neither -json nor a row count fits.
func Text(path, stmt string, args ...string) (string, error) {
	bound, err := Bind(stmt, args...)
	if err != nil {
		return "", err
	}
	out, err := run(path, []string{"-noheader", "-bail"}, bound)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Exec runs one or more writing statements. Unlike the ad-hoc
// `exec.Command(...).Run()` calls it replaces, a failure is returned: a switch
// that could not write must not report success.
func Exec(path, stmt string, args ...string) error {
	bound, err := Bind(stmt, args...)
	if err != nil {
		return err
	}
	_, err = run(path, []string{"-bail"}, bound)
	return err
}

// ExecScript runs statements that are already complete SQL, with no
// placeholder substitution. It exists for replaying a script this package
// generated earlier (the Claude cookie restore), where a literal `?` inside a
// stored value must not be mistaken for a placeholder.
func ExecScript(path, script string) error {
	_, err := run(path, []string{"-bail"}, script)
	return err
}

// Backup writes a consistent copy of the database to dest using sqlite3's own
// .backup, which is safe against a concurrent writer in a way `cp` is not.
//
// .backup is a dot command, not SQL, so its argument cannot be a bound value —
// it is single-quoted, and a destination that could break out of those quotes
// is rejected rather than escaped.
func Backup(path, dest string) error {
	if strings.ContainsAny(dest, "'\n\r") {
		return fmt.Errorf("sqlite backup: destination path may not contain a quote or newline: %q", dest)
	}
	_, err := run(path, []string{"-bail"}, ".backup '"+dest+"'\n")
	return err
}

// ExecCount runs a writing statement and returns how many rows it changed, so
// a caller can report what was actually deleted rather than assuming the
// statement applied.
func ExecCount(path, stmt string, args ...string) (int, error) {
	bound, err := Bind(stmt, args...)
	if err != nil {
		return 0, err
	}
	out, err := run(path, []string{"-noheader", "-bail"}, bound+"\nSELECT changes();\n")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return 0, fmt.Errorf("sqlite3 %s: no row count returned", filepath.Base(path))
	}
	n, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil {
		return 0, fmt.Errorf("sqlite3 %s: unreadable row count %q: %w", filepath.Base(path), fields[len(fields)-1], err)
	}
	return n, nil
}
