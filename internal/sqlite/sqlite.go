// Package sqlite runs SQLite statements through the sqlite3 binary without
// delimiter-based text parsing and without hand-rolled SQL escaping.
//
// Reads go through [Query], which asks for -json and decodes into a typed
// struct. Writes go through [Exec]. Both take values as arguments bound in
// place of `?` placeholders, so no caller has to double quotes itself.
//
// Values are substituted as hex blob literals cast to text rather than as
// quoted strings, which cannot be escaped out of. The one value this cannot
// carry is a NUL byte: SQLite treats it as the end of the text. Nothing this
// project stores contains one, and the previous ”-doubling broke on it too.
package sqlite

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// timeout bounds every invocation. A database locked by a running IDE is the
// expected failure here, and it must surface as an error rather than hang.
const timeout = 10 * time.Second

var (
	binaryOnce sync.Once
	binaryPath string
)

// Binary resolves the sqlite3 executable. PATH first, then the locations it is
// installed to on this class of host, so an immutable OS whose PATH omits
// linuxbrew still works. Resolved once per process.
func Binary() string {
	binaryOnce.Do(func() {
		if p, err := exec.LookPath("sqlite3"); err == nil {
			binaryPath = p
			return
		}
		home, _ := os.UserHomeDir()
		for _, c := range []string{
			"/var/home/linuxbrew/.linuxbrew/bin/sqlite3",
			filepath.Join(home, ".local/bin/sqlite3"),
			filepath.Join(home, ".gemini/antigravity-ide/bin/sqlite3"),
			"/usr/bin/sqlite3",
			"/bin/sqlite3",
		} {
			if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
				binaryPath = c
				return
			}
		}
		// Left as the bare name so the failure names the missing binary
		// instead of a path that was only ever a guess.
		binaryPath = "sqlite3"
	})
	return binaryPath
}

// Literal renders a Go string as a SQL expression. Exported because a few
// statements are assembled for later replay (the cookie restore) rather than
// executed immediately.
func Literal(s string) string {
	if s == "" {
		return "''"
	}
	return "CAST(x'" + hex.EncodeToString([]byte(s)) + "' AS TEXT)"
}

// Bind substitutes args for the `?` placeholders in stmt, leftmost first.
// Placeholders inside single-quoted string literals are left alone. It is an
// error for the counts to disagree: a statement with an unfilled placeholder
// would otherwise reach sqlite3 and fail there with a parse error that says
// nothing about the real mistake.
func Bind(stmt string, args ...string) (string, error) {
	var b strings.Builder
	b.Grow(len(stmt) + len(args)*16)

	inString := false
	used := 0
	for i := 0; i < len(stmt); i++ {
		c := stmt[i]
		switch {
		case inString:
			b.WriteByte(c)
			if c == '\'' {
				inString = false
			}
		case c == '\'':
			b.WriteByte(c)
			inString = true
		case c == '?':
			if used >= len(args) {
				return "", fmt.Errorf("sqlite bind: statement has more placeholders than the %d arguments given", len(args))
			}
			b.WriteString(Literal(args[used]))
			used++
		default:
			b.WriteByte(c)
		}
	}
	if used != len(args) {
		return "", fmt.Errorf("sqlite bind: statement has %d placeholders but %d arguments were given", used, len(args))
	}
	return b.String(), nil
}

// run invokes sqlite3 with the given leading flags and statement, returning
// stdout. stderr is folded into the error so a locked or missing database says
// which it was.
func run(path string, flags []string, stmt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args := append(append([]string{}, flags...), path)
	cmd := exec.CommandContext(ctx, Binary(), args...)
	// The statement goes in on stdin, not argv: a transcript-sized UPDATE
	// would otherwise risk the argument-length limit.
	cmd.Stdin = strings.NewReader(stmt)

	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return "", fmt.Errorf("sqlite3 %s: %w: %s", filepath.Base(path), err, detail)
		}
		return "", fmt.Errorf("sqlite3 %s: %w", filepath.Base(path), err)
	}
	return string(out), nil
}
