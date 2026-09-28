package desktop

import (
	"strings"
	"testing"
)

// The two hand-written entries this package replaced interpolated a path
// straight into Exec=, so an install directory containing a space produced a
// launcher that ran the wrong program with the wrong arguments.
func TestExecLineQuotesArgumentsContainingSpaces(t *testing.T) {
	got := Entry{Exec: []string{"/opt/my tools/ai", "account", "switch", "my profile"}}.ExecLine()
	want := `"/opt/my tools/ai" account switch "my profile" %F`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExecLineEscapesReservedCharacters(t *testing.T) {
	got := Entry{Exec: []string{`/bin/ai`, `a"b`, `c$d`, `e\f`}}.ExecLine()
	want := `/bin/ai "a\"b" "c\$d" "e\\f" %F`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRenderEmitsRequiredKeys(t *testing.T) {
	out := Entry{
		Name: "Claude (work)",
		Exec: []string{"/bin/ai", "account", "switch", "work"},
		Icon: "/icons/claude.png",
	}.Render()

	for _, want := range []string{"[Desktop Entry]", "Type=Application", "Name=Claude (work)", "Exec=/bin/ai account switch work %F", "Icon=/icons/claude.png"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered entry is missing %q:\n%s", want, out)
		}
	}
}
