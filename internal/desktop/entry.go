// Package desktop writes freedesktop.org .desktop launchers. It exists so the
// quoting rules are applied once: both providers wrote their own entry, and
// both interpolated an unquoted path into Exec=, which breaks for any install
// directory containing a space.
package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Entry is one launcher.
type Entry struct {
	// FileName is the basename written under ~/Desktop, without .desktop.
	FileName string
	Name     string
	Comment  string
	// Exec is the program, followed by its arguments as separate elements.
	// Each is quoted here rather than by the caller.
	Exec       []string
	Icon       string
	WMClass    string
	Categories string
}

// quoteArg applies the Desktop Entry Specification's quoting: an argument
// containing a reserved character is double-quoted, and a backslash, quote,
// backtick or dollar inside it is escaped.
func quoteArg(arg string) string {
	if arg != "" && !strings.ContainsAny(arg, " \t\n\"'\\><~|&;$*?#()`") {
		return arg
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range arg {
		if strings.ContainsRune(`"`+"`"+`$\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

// ExecLine renders the Exec= value. %F is appended so a file manager can open
// files with the entry.
func (e Entry) ExecLine() string {
	parts := make([]string, 0, len(e.Exec)+1)
	for _, a := range e.Exec {
		parts = append(parts, quoteArg(a))
	}
	return strings.Join(parts, " ") + " %F"
}

// Render produces the file contents.
func (e Entry) Render() string {
	var b strings.Builder
	b.WriteString("[Desktop Entry]\n")
	b.WriteString("Type=Application\n")
	fmt.Fprintf(&b, "Name=%s\n", e.Name)
	if e.Comment != "" {
		fmt.Fprintf(&b, "Comment=%s\n", e.Comment)
	}
	fmt.Fprintf(&b, "Exec=%s\n", e.ExecLine())
	if e.Icon != "" {
		fmt.Fprintf(&b, "Icon=%s\n", e.Icon)
	}
	if e.WMClass != "" {
		fmt.Fprintf(&b, "StartupWMClass=%s\n", e.WMClass)
	}
	if e.Categories != "" {
		fmt.Fprintf(&b, "Categories=%s\n", e.Categories)
	}
	b.WriteString("StartupNotify=false\nTerminal=false\n")
	return b.String()
}

// WriteToDesktop writes the entry to the user's Desktop directory. 0755
// because some file managers refuse to run an entry that is not executable.
func (e Entry) WriteToDesktop() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, "Desktop")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, e.FileName+".desktop")
	return os.WriteFile(path, []byte(e.Render()), 0755)
}
