package provider

import (
	"path/filepath"
	"strings"
)

// Workspace is a conversation's project directory.
//
// It exists because the same value was carried as a bare string and normalised
// at ten call sites, five of which cleaned the path and five of which only
// stripped the scheme. So `history --workspace` and the project_path written
// into Qdrant could disagree about the same directory: a trailing slash or a
// ".." survived into the payload and then never matched a filter built from a
// cleaned path.
//
// Providers store the directory in whatever form their on-disk data uses —
// Antigravity writes a file:// URI, the other two a plain path — and this parses
// either into one normalised value.
type Workspace struct {
	path string
}

// ParseWorkspace normalises a workspace directory from a provider.
func ParseWorkspace(raw string) Workspace {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Workspace{}
	}
	trimmed = strings.TrimPrefix(trimmed, "file://")
	cleaned := filepath.Clean(trimmed)
	// filepath.Clean turns an empty or relative-only value into ".", which is
	// not a workspace; treating it as one would match every conversation.
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return Workspace{}
	}
	return Workspace{path: cleaned}
}

// Path is the normalised absolute-or-relative directory, or "" when unknown.
// This is the form stored and filtered on, so both sides agree.
func (w Workspace) Path() string { return w.path }

// Known reports whether a directory was recorded at all.
func (w Workspace) Known() bool { return w.path != "" }

// Display renders the workspace for output, as "-" when unknown so an absent
// directory is never shown as an empty cell that reads like a bug.
func (w Workspace) Display() string {
	if w.path == "" {
		return "-"
	}
	return w.path
}

// String makes Workspace printable; it is Path, not Display, so a formatted
// value never smuggles a "-" into stored data.
func (w Workspace) String() string { return w.path }

// Contains reports whether this workspace and other are the same directory, or
// one is inside the other.
//
// The relation is deliberately symmetric: `history --workspace <repo>` is meant
// to show the conversations held in that directory and in any parent of it, so a
// conversation recorded against the repository root matches a query for a
// subdirectory and the other way round.
func (w Workspace) Contains(other Workspace) bool {
	if !w.Known() || !other.Known() {
		return false
	}
	if w.path == other.path {
		return true
	}
	return strings.HasPrefix(other.path, w.path+string(filepath.Separator)) ||
		strings.HasPrefix(w.path, other.path+string(filepath.Separator))
}
