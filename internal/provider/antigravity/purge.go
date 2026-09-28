package antigravity

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"agentcli.local/ai/internal/proc"
	"agentcli.local/ai/internal/provider"
	"agentcli.local/ai/internal/sqlite"
)

// paths holds the Antigravity directories one conversation is spread across.
type paths struct {
	home     string
	db       string
	brainDir string
	convDir  string
}

func resolvePaths() (paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return paths{}, err
	}
	return paths{
		home:     home,
		db:       filepath.Join(home, ".config/Antigravity IDE/User/globalStorage/state.vscdb"),
		brainDir: filepath.Join(home, ".gemini/antigravity-ide/brain"),
		convDir:  filepath.Join(home, ".gemini/antigravity-ide/conversations"),
	}, nil
}

// PurgeConversation implements provider.ConversationPurger.
//
// An Antigravity conversation is a brain directory of artifacts and
// transcripts, a per-conversation .db beside it, and an entry in the
// trajectorySummaries index inside state.vscdb. The index entry is what the
// IDE lists from, so removing the files without it leaves a conversation that
// appears in the list and opens to nothing.
func (p *AntigravityProvider) PurgeConversation(id string, opts provider.PurgeOptions) (*provider.Purge, error) {
	summary, err := provider.Find(p, id)
	if err != nil {
		return nil, err
	}
	loc, err := resolvePaths()
	if err != nil {
		return nil, err
	}

	// The IDE holds state.vscdb open and rewrites the index from memory when it
	// exits, which would restore the entry that was just deleted.
	if !opts.DryRun && proc.Running(antigravityProcessPattern) {
		return nil, fmt.Errorf("antigravity-ide is still running; stop it before purging conversations, or it will rewrite the index")
	}

	purge := purgeFiles(p.Name(), loc, *summary, opts)
	if err := dropIndexEntries(&purge, loc, map[string]bool{summary.ID: true}, opts); err != nil {
		purge.Warnf("%v", err)
	}
	if len(purge.RemovedPaths) == 0 && purge.RemovedIndexEntries == 0 {
		purge.Warnf("found no stored data for conversation %s", summary.ID)
	}
	return &purge, nil
}

// PurgeAllConversations implements provider.ConversationPurger. The index is
// rewritten once for the whole set rather than once per conversation, so a bulk
// purge does not re-encode and re-write it hundreds of times.
func (p *AntigravityProvider) PurgeAllConversations(opts provider.PurgeOptions) ([]provider.Purge, error) {
	summaries, err := p.ListConversations(provider.HistoryOptions{})
	if err != nil {
		return nil, err
	}
	loc, err := resolvePaths()
	if err != nil {
		return nil, err
	}
	if !opts.DryRun && proc.Running(antigravityProcessPattern) {
		return nil, fmt.Errorf("antigravity-ide is still running; stop it before purging conversations, or it will rewrite the index")
	}

	purges := make([]provider.Purge, 0, len(summaries))
	ids := make(map[string]bool, len(summaries))
	for _, summary := range summaries {
		purges = append(purges, purgeFiles(p.Name(), loc, summary, opts))
		ids[summary.ID] = true
	}

	// The index rewrite is one operation for the set, so its outcome is
	// attributed to the first purge rather than invented for each.
	shared := provider.Purge{Provider: p.Name(), DryRun: opts.DryRun}
	if err := dropIndexEntries(&shared, loc, ids, opts); err != nil {
		shared.Warnf("%v", err)
	}
	if len(purges) > 0 {
		purges[0].RemovedIndexEntries += shared.RemovedIndexEntries
		purges[0].BackupPath = shared.BackupPath
		purges[0].Warnings = append(purges[0].Warnings, shared.Warnings...)
	}
	return purges, nil
}

// purgeFiles removes one conversation's on-disk data.
func purgeFiles(providerName string, loc paths, summary provider.ConversationSummary, opts provider.PurgeOptions) provider.Purge {
	purge := provider.Purge{
		Provider: providerName,
		ID:       summary.ID,
		Title:    summary.Title,
		DryRun:   opts.DryRun,
	}
	purge.RemovePath(loc.brainDir, filepath.Join(loc.brainDir, summary.ID), opts)
	purge.RemovePath(loc.convDir, filepath.Join(loc.convDir, summary.ID+".db"), opts)
	return purge
}

// dropIndexEntries rewrites the trajectorySummaries index without the given
// conversation ids.
//
// The index is a protobuf map re-encoded from the entries that remain, the same
// way Recover assembles it, so nothing here has to understand an entry's
// contents — only which key it is under.
func dropIndexEntries(purge *provider.Purge, loc paths, ids map[string]bool, opts provider.PurgeOptions) error {
	raw, err := readVscdbB64Proto(loc.db, trajectorySummariesKey)
	if err != nil {
		// A missing key means there is no index to edit, which is not a
		// failure: the files have still gone.
		return nil
	}
	entries, err := extractMapEntries(raw)
	if err != nil {
		return fmt.Errorf("reading the trajectory index: %w", err)
	}

	present := 0
	for id := range ids {
		if _, ok := entries[id]; ok {
			present++
		}
	}
	if present == 0 {
		return nil
	}
	if opts.DryRun {
		purge.RemovedIndexEntries += present
		return nil
	}

	// Sorted so the rewritten index is byte-stable for a given set of entries.
	remaining := make([]string, 0, len(entries))
	for id := range entries {
		if !ids[id] {
			remaining = append(remaining, id)
		}
	}
	sort.Strings(remaining)

	rebuilt := new(bytes.Buffer)
	for _, id := range remaining {
		rebuilt.Write(encodeFieldBytes(1, entries[id]))
	}

	// A backup first: this rewrites an index the IDE depends on, and an
	// interrupted write would lose every other conversation too. It is removed
	// again unless the caller asked to keep it, because leaving a copy of the
	// data the user asked to erase would defeat the request.
	backupPath := fmt.Sprintf("%s.purge-backup.%d", loc.db, time.Now().Unix())
	if err := sqlite.Backup(loc.db, backupPath); err != nil {
		return fmt.Errorf("backing up %s before editing the index: %w", filepath.Base(loc.db), err)
	}

	updated := base64.StdEncoding.EncodeToString(rebuilt.Bytes())
	if err := sqlite.Exec(loc.db, "UPDATE ItemTable SET value = ? WHERE key = ?;", updated, trajectorySummariesKey); err != nil {
		purge.BackupPath = backupPath
		return fmt.Errorf("rewriting the trajectory index (the previous index is at %s): %w", backupPath, err)
	}

	if opts.KeepBackup {
		purge.BackupPath = backupPath
	} else if err := os.Remove(backupPath); err != nil {
		purge.Warnf("removing the temporary index backup %s: %v", backupPath, err)
	}
	purge.RemovedIndexEntries += present
	return nil
}

var _ provider.ConversationPurger = (*AntigravityProvider)(nil)
