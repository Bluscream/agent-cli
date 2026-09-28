package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agentcli.local/ai/internal/idutil"
)

// Find returns the one conversation of p matching id, which may be a full id, a
// short id, or a prefix of either. It is the shared preamble of every
// GetConversation, AuditConversation and PurgeConversation: each provider had
// written its own copy, and they disagreed about what "not found" looked like.
//
// The search is narrowed with HistoryOptions.Search first, so a provider that
// can filter cheaply does, and then matched properly — Search is a substring
// hint, not a guarantee of uniqueness.
func Find(p Provider, id string) (*ConversationSummary, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%s: no conversation id given", p.Name())
	}
	summaries, err := p.ListConversations(HistoryOptions{Search: id})
	if err != nil {
		return nil, err
	}
	for i := range summaries {
		if idutil.Match(id, summaries[i].ID) {
			// Indexed rather than ranged over: taking the address of the loop
			// variable would return whichever element the loop ended on.
			return &summaries[i], nil
		}
	}
	return nil, fmt.Errorf("%s conversation not found: %s", p.Name(), id)
}

// Latest returns p's most recently active conversation.
func Latest(p Provider) (*ConversationSummary, error) {
	recent, err := p.ListConversations(HistoryOptions{Last: true})
	if err != nil {
		return nil, err
	}
	if len(recent) == 0 {
		return nil, fmt.Errorf("no recent %s conversation found", p.DisplayName())
	}
	return &recent[0], nil
}

// Endpoints returns a conversation's opening user message and its last
// substantive assistant message. Four copies of this fold existed — one per
// provider and one for the remote read path — and they agreed, which is the
// argument for having one.
func Endpoints(turns []TurnInfo) (initialPrompt, lastResponse string) {
	for _, turn := range turns {
		if turn.Role == "user" && initialPrompt == "" {
			initialPrompt = turn.Content
		}
		if turn.Role == "assistant" && strings.TrimSpace(turn.Content) != "" {
			lastResponse = turn.Content
		}
	}
	return initialPrompt, lastResponse
}

// taskFiles are the files an audit reads pending work from, in preference
// order, when the provider has no better source.
var taskFiles = []string{"TODO.md", "task_list.md", "AGENTS.md"}

// AuditTasker is an optional capability: a provider that knows a better source
// of a conversation's pending work than the workspace's task files. Antigravity
// writes an implementation plan as a conversation artifact, which says what
// that conversation still has to do rather than what the repository does.
type AuditTasker interface {
	PendingTasks(detail *ConversationDetail) []string
}

// Dossier builds the audit record for a conversation already read. Every
// provider built this by hand, and the copies had drifted: Claude's omitted
// TouchedArtifacts entirely, so an audit of a Claude conversation reported no
// artifacts rather than the artifacts it had.
func Dossier(p Provider, detail *ConversationDetail) *AuditDossier {
	prompt, response := Endpoints(detail.Turns)
	if prompt == "" {
		prompt = detail.InitialPrompt
	}
	if prompt == "" {
		prompt = detail.Summary.Title
	}
	if response == "" {
		response = detail.LastResponse
	}

	dossier := &AuditDossier{
		Provider:           p.Name(),
		ConversationID:     detail.Summary.ID,
		Title:              detail.Summary.Title,
		LastActive:         detail.Summary.UpdatedAt,
		WorkspaceDir:       detail.Summary.WorkspaceDir,
		WorkspaceGit:       detail.WorkspaceGit,
		FullTranscriptPath: detail.TranscriptPath,
		UserPrompt:         prompt,
		AssistantSummary:   response,
		TouchedArtifacts:   detail.Artifacts,
	}

	if tasker, ok := p.(AuditTasker); ok {
		dossier.PendingOpenTasks = tasker.PendingTasks(detail)
	}
	if len(dossier.PendingOpenTasks) == 0 && detail.WorkspaceGit.IsRepo {
		for _, name := range taskFiles {
			data, err := os.ReadFile(filepath.Join(detail.WorkspaceGit.WorkDir, name))
			if err != nil {
				continue
			}
			dossier.PendingOpenTasks = PlanTasks(string(data))
			break
		}
	}
	return dossier
}

// Audit reads a conversation and builds its dossier. An empty id means the
// most recent conversation.
func Audit(p Provider, id string) (*AuditDossier, error) {
	if id == "" {
		latest, err := Latest(p)
		if err != nil {
			return nil, err
		}
		id = latest.ID
	}
	detail, err := p.GetConversation(id)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, fmt.Errorf("%s conversation not found: %s", p.Name(), id)
	}
	return Dossier(p, detail), nil
}

// PlanTasks extracts unchecked markdown checkbox items. It was byte-identical
// in the Claude and Antigravity packages.
func PlanTasks(md string) []string {
	var tasks []string
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ]") || strings.HasPrefix(trimmed, "* [ ]") {
			tasks = append(tasks, strings.TrimSpace(trimmed[5:]))
		}
	}
	return tasks
}

// ExistingDirs keeps only the paths that are directories today. Every
// TranscriptRoots implementation needs this, and each had written it out again.
func ExistingDirs(paths ...string) []string {
	var found []string
	for _, path := range paths {
		if fi, err := os.Stat(path); err == nil && fi.IsDir() {
			found = append(found, path)
		}
	}
	return found
}

// SkillDescription reads a skill's `description:` out of its YAML frontmatter.
// It was byte-identical in all three provider packages.
func SkillDescription(content string) string {
	inFrontmatter := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if inFrontmatter {
				return ""
			}
			inFrontmatter = true
			continue
		}
		if inFrontmatter && strings.HasPrefix(trimmed, "description:") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "description:"))
		}
	}
	return ""
}
