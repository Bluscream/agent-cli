package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"agentcli.local/ai/internal/provider"
)

// PurgeConversation implements provider.ConversationPurger.
//
// A Claude conversation is three things on disk: the JSONL transcript under
// ~/.claude/projects/<workspace>/, a sidecar directory of the same name holding
// per-conversation state such as the custom title, and any desktop session
// file under ~/.config/Claude/claude-code-sessions that names the same id.
// All three go, or the purge reports which did not.
func (p *ClaudeProvider) PurgeConversation(id string, opts provider.PurgeOptions) (*provider.Purge, error) {
	summary, err := provider.Find(p, id)
	if err != nil {
		return nil, err
	}
	purge := p.purgeSummary(*summary, opts)
	return &purge, nil
}

// PurgeAllConversations implements provider.ConversationPurger.
func (p *ClaudeProvider) PurgeAllConversations(opts provider.PurgeOptions) ([]provider.Purge, error) {
	summaries, err := p.ListConversations(provider.HistoryOptions{})
	if err != nil {
		return nil, err
	}
	purges := make([]provider.Purge, 0, len(summaries))
	for _, summary := range summaries {
		purges = append(purges, p.purgeSummary(summary, opts))
	}
	return purges, nil
}

// purgeSummary erases one already-resolved conversation. It records every
// problem as a warning rather than returning, so one unremovable file does not
// leave the rest of a bulk purge undone.
func (p *ClaudeProvider) purgeSummary(summary provider.ConversationSummary, opts provider.PurgeOptions) provider.Purge {
	purge := provider.Purge{
		Provider: p.Name(),
		ID:       summary.ID,
		Title:    summary.Title,
		DryRun:   opts.DryRun,
	}

	home, err := os.UserHomeDir()
	if err != nil {
		purge.Warnf("resolving home directory: %v", err)
		return purge
	}
	projectsDir := filepath.Join(home, ".claude/projects")

	transcript := summary.TranscriptPath
	if transcript == "" {
		transcript = findTranscript(summary.ID)
	}
	if transcript != "" {
		purge.RemovePath(projectsDir, transcript, opts)
		// The sidecar directory sits beside the transcript under the same
		// name; it holds the custom title and would otherwise keep the
		// conversation visible in the desktop app's list.
		purge.RemovePath(projectsDir, strings.TrimSuffix(transcript, ".jsonl"), opts)
	}

	for _, path := range desktopSessionFiles(home, summary.ID) {
		purge.RemovePath(filepath.Join(home, ".config/Claude/claude-code-sessions"), path, opts)
	}

	if len(purge.RemovedPaths) == 0 {
		purge.Warnf("found no stored data for conversation %s", summary.ID)
	}
	return purge
}

// desktopSessionFiles finds the desktop app's session files for one
// conversation. The id appears either as the filename or inside the document,
// which is how ListConversations reads them, so both are matched here.
func desktopSessionFiles(home, sessionID string) []string {
	root := filepath.Join(home, ".config/Claude/claude-code-sessions")
	var found []string
	_ = filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		if strings.TrimSuffix(filepath.Base(path), ".json") == sessionID {
			found = append(found, path)
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var doc struct {
			SessionID    string `json:"sessionId"`
			CLISessionID string `json:"cliSessionId"`
		}
		if json.Unmarshal(data, &doc) == nil && (doc.SessionID == sessionID || doc.CLISessionID == sessionID) {
			found = append(found, path)
		}
		return nil
	})
	return found
}

// compile-time check that the capability is satisfied.
var _ provider.ConversationPurger = (*ClaudeProvider)(nil)
