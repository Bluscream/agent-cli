package provider

import (
	"fmt"
	"sort"
	"time"
)

// PurgeOptions controls a conversation deletion.
type PurgeOptions struct {
	// DryRun reports exactly what would be removed without removing it. Every
	// implementation must fill the same fields it would on a real run, so a
	// dry run is a usable preview rather than a different code path.
	DryRun bool
	// KeepBackup asks providers that edit a database to leave the backup copy
	// they take beforehand. Off by default: a backup of a database the user
	// asked to have a conversation erased from would defeat the request.
	KeepBackup bool
}

// Purge records what removing one conversation did, or would do.
type Purge struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
	Title    string `json:"title,omitempty"`
	DryRun   bool   `json:"dry_run,omitempty"`

	// RemovedPaths are the files and directories deleted, in the order they
	// were deleted. Present on a dry run too, as the paths that would go.
	RemovedPaths []string `json:"removed_paths,omitempty"`
	// RemovedRows counts database rows deleted.
	RemovedRows int `json:"removed_rows,omitempty"`
	// RemovedIndexEntries counts entries removed from a provider's own
	// conversation index, which is separate from the transcript itself: a
	// transcript deleted without its index entry leaves a listing that names a
	// conversation nobody can open.
	RemovedIndexEntries int `json:"removed_index_entries,omitempty"`
	// FreedBytes is the disk space released.
	FreedBytes int64 `json:"freed_bytes"`
	// BackupPath names the database backup taken first, when one was kept.
	BackupPath string `json:"backup_path,omitempty"`

	// Warnings record parts that could not be removed. A purge that half
	// succeeded reports which half; it does not present itself as complete.
	Warnings []string `json:"warnings,omitempty"`
}

// Complete reports whether everything the purge attempted succeeded.
func (p Purge) Complete() bool { return len(p.Warnings) == 0 }

// Warnf appends a warning. Provider implementations use it instead of
// returning, so one unremovable artifact does not abandon the rest.
func (p *Purge) Warnf(format string, args ...any) {
	p.Warnings = append(p.Warnings, fmt.Sprintf(format, args...))
}

// ConversationPurger is an optional capability: a provider that can erase a
// conversation's local data. It is separate from Provider so a provider whose
// storage this tool does not fully understand simply does not implement it,
// rather than offering a delete that leaves half the data behind.
//
// An implementation must remove every local trace it knows of — transcript,
// index entry, artifacts and per-conversation database — so that the
// conversation no longer appears in ListConversations afterwards.
type ConversationPurger interface {
	// PurgeConversation erases one conversation. The id may be a short id or
	// prefix, matched the same way GetConversation matches it. An id that
	// matches nothing is an error, not a silent success: the caller asked for
	// something specific to be gone and is entitled to know it was not there.
	PurgeConversation(id string, opts PurgeOptions) (*Purge, error)

	// PurgeAllConversations erases every conversation this provider stores. It
	// returns one Purge per conversation, so a partial failure names which
	// conversations survived.
	PurgeAllConversations(opts PurgeOptions) ([]Purge, error)
}

// PurgeSummary aggregates purges across providers.
type PurgeSummary struct {
	DryRun        bool      `json:"dry_run"`
	Conversations int       `json:"conversations"`
	Removed       int       `json:"removed"`
	Failed        int       `json:"failed"`
	FreedBytes    int64     `json:"freed_bytes"`
	StartedAt     time.Time `json:"started_at"`
	Duration      string    `json:"duration"`
	// Unsupported names providers that cannot delete conversations, so "all
	// providers" never silently means "the ones that happened to support it".
	Unsupported []string `json:"unsupported_providers,omitempty"`
	Purges      []Purge  `json:"purges,omitempty"`
}

// Add folds one purge into the summary.
func (s *PurgeSummary) Add(purge Purge) {
	s.Conversations++
	s.FreedBytes += purge.FreedBytes
	if purge.Complete() {
		s.Removed++
	} else {
		s.Failed++
	}
	s.Purges = append(s.Purges, purge)
}

// Sort orders the purges by provider then id so output is stable.
func (s *PurgeSummary) Sort() {
	sort.Slice(s.Purges, func(i, j int) bool {
		if s.Purges[i].Provider != s.Purges[j].Provider {
			return s.Purges[i].Provider < s.Purges[j].Provider
		}
		return s.Purges[i].ID < s.Purges[j].ID
	})
	sort.Strings(s.Unsupported)
}
