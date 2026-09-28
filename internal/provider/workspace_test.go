package provider

import (
	"path/filepath"
	"testing"
)

func TestParseWorkspaceNormalisesSchemeAndPath(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"file:///run/media/project", "/run/media/project"},
		{"/run/media/project", "/run/media/project"},
		// The forms that used to survive into a Qdrant payload and then never
		// match a filter built from a cleaned path.
		{"/run/media/project/", "/run/media/project"},
		{"/run/media/other/../project", "/run/media/project"},
		{"file:///run/media/project//", "/run/media/project"},
		{"  /run/media/project  ", "/run/media/project"},
		{"", ""},
		{"file://", ""},
		{".", ""},
		{"/", ""},
	}
	for _, c := range cases {
		if got := ParseWorkspace(c.raw).Path(); got != c.want {
			t.Errorf("ParseWorkspace(%q).Path() = %q, want %q", c.raw, got, c.want)
		}
	}
}

// The whole point of the type: the two spellings of one directory must produce
// the same stored value.
func TestParseWorkspaceAgreesAcrossSpellings(t *testing.T) {
	a := ParseWorkspace("file:///run/media/project/")
	b := ParseWorkspace("/run/media/project")
	if a.Path() != b.Path() {
		t.Fatalf("%q and %q normalised differently", a.Path(), b.Path())
	}
}

func TestWorkspaceDisplayMarksAnUnknownDirectory(t *testing.T) {
	if got := ParseWorkspace("").Display(); got != "-" {
		t.Fatalf("got %q, want -", got)
	}
	if ParseWorkspace("").Known() {
		t.Fatal("an empty workspace reported itself as known")
	}
}

func TestWorkspaceContainsIsSymmetricAcrossParentAndChild(t *testing.T) {
	parent := ParseWorkspace("/run/media/project")
	child := ParseWorkspace("/run/media/project/internal/cli")

	if !parent.Contains(child) {
		t.Error("a parent does not contain its child")
	}
	if !child.Contains(parent) {
		t.Error("a child does not match a query for its parent")
	}
	if !parent.Contains(parent) {
		t.Error("a workspace does not contain itself")
	}
}

// A shared name prefix is not containment: /project-old is not inside /project.
func TestWorkspaceContainsRejectsASharedNamePrefix(t *testing.T) {
	a := ParseWorkspace("/run/media/project")
	b := ParseWorkspace("/run/media/project-old")
	if a.Contains(b) || b.Contains(a) {
		t.Fatalf("%q and %q were treated as nested", a.Path(), b.Path())
	}
}

func TestWorkspaceContainsIsFalseWhenEitherIsUnknown(t *testing.T) {
	known := ParseWorkspace(filepath.Join("/run", "media"))
	if known.Contains(Workspace{}) || (Workspace{}).Contains(known) {
		t.Fatal("an unknown workspace matched a known one")
	}
}
