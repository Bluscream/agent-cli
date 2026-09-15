package search

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
)

// MatchResult represents a single search match across conversations, memories, or skills.
type MatchResult struct {
	Type        string    `json:"type"`                   // "conversation", "memory", "skill"
	Provider    string    `json:"provider"`               // "antigravity", "claude", "codex"
	EntityID    string    `json:"entity_id"`              // Full ID or Short ID
	EntityTitle string    `json:"entity_title,omitempty"` // Title of conversation/memory/skill
	Workspace   string    `json:"workspace,omitempty"`    // Workspace directory if applicable
	Author      string    `json:"author,omitempty"`       // "user", "assistant", "system", etc.
	Timestamp   time.Time `json:"timestamp"`              // When it occurred / updated
	Location    string    `json:"location,omitempty"`     // e.g. "Turn #5", "Content", "SKILL.md:12"
	Snippet     string    `json:"snippet"`                // Surrounding text excerpt
}

// Options defines the search criteria.
type Options struct {
	Text          string
	Pattern       string
	Workspace     string
	Provider      string
	TypeFilter    string // "conversations", "memories", "skills", or empty for all
	Limit         int
	CaseSensitive bool
}

// Matcher encapsulates string or regex matching and context snippet generation.
type Matcher struct {
	isRegex bool
	re      *regexp.Regexp
	needle  string
}

// NewMatcher builds a matcher from text or regex pattern.
func NewMatcher(text, pattern string, caseSensitive bool) (*Matcher, error) {
	if pattern != "" {
		expr := pattern
		if !caseSensitive && !strings.HasPrefix(expr, "(?i)") {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("invalid regex pattern: %w", err)
		}
		return &Matcher{isRegex: true, re: re}, nil
	}

	if text == "" {
		return nil, fmt.Errorf("either search text or regex pattern must be provided")
	}

	needle := text
	if !caseSensitive {
		needle = strings.ToLower(text)
	}
	return &Matcher{isRegex: false, needle: needle}, nil
}

// QuickBytesMatch quickly tests whether raw byte data could match.
func (m *Matcher) QuickBytesMatch(data []byte) bool {
	if m.isRegex {
		return m.re.Match(data)
	}
	// Case-insensitive ASCII substring search
	needle := []byte(m.needle)
	if len(needle) == 0 {
		return false
	}
	return bytes.Contains(bytes.ToLower(data), needle)
}

// FindMatch tests whether target matches and returns a snippet around the match.
func (m *Matcher) FindMatch(content string, maxSnippetLen int) (bool, string) {
	if maxSnippetLen <= 0 {
		maxSnippetLen = 140
	}

	var start, end int
	if m.isRegex {
		loc := m.re.FindStringIndex(content)
		if loc == nil {
			return false, ""
		}
		start, end = loc[0], loc[1]
	} else {
		idx := strings.Index(strings.ToLower(content), m.needle)
		if idx == -1 {
			return false, ""
		}
		start, end = idx, idx+len(m.needle)
	}

	matchLen := end - start
	half := (maxSnippetLen - matchLen) / 2
	if half < 15 {
		half = 15
	}

	sIdx := start - half
	if sIdx < 0 {
		sIdx = 0
	}
	eIdx := end + half
	if eIdx > len(content) {
		eIdx = len(content)
	}

	raw := content[sIdx:eIdx]
	clean := strings.ReplaceAll(raw, "\n", " ")
	clean = strings.ReplaceAll(clean, "\r", "")
	clean = strings.Join(strings.Fields(clean), " ")

	prefix := ""
	if sIdx > 0 {
		prefix = "..."
	}
	suffix := ""
	if eIdx < len(content) {
		suffix = "..."
	}

	return true, prefix + clean + suffix
}

// Execute performs the search across conversations, memories, and skills according to Options.
func Execute(opts Options) ([]MatchResult, error) {
	matcher, err := NewMatcher(opts.Text, opts.Pattern, opts.CaseSensitive)
	if err != nil {
		return nil, err
	}

	var provList []provider.Provider
	if opts.Provider != "" {
		p, err := provider.Get(opts.Provider)
		if err != nil {
			return nil, err
		}
		provList = []provider.Provider{p}
	} else {
		provList = provider.All()
	}

	searchConvos := opts.TypeFilter == "" || strings.EqualFold(opts.TypeFilter, "conversations") || strings.EqualFold(opts.TypeFilter, "conversation")
	searchMemories := opts.TypeFilter == "" || strings.EqualFold(opts.TypeFilter, "memories") || strings.EqualFold(opts.TypeFilter, "memory")
	searchSkills := opts.TypeFilter == "" || strings.EqualFold(opts.TypeFilter, "skills") || strings.EqualFold(opts.TypeFilter, "skill")

	var results []MatchResult
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}

	// 1. Search Conversations
	if searchConvos {
		histOpts := provider.HistoryOptions{
			Workspace: opts.Workspace,
		}
		for _, p := range provList {
			convos, err := p.ListConversations(histOpts)
			if err != nil {
				continue
			}

			cleanTarget := ""
			if opts.Workspace != "" {
				cleanTarget = filepath.Clean(strings.TrimPrefix(opts.Workspace, "file://"))
			}

			for _, c := range convos {
				if cleanTarget != "" {
					ws := filepath.Clean(strings.TrimPrefix(c.WorkspaceDir, "file://"))
					if ws == "." || ws == "" {
						continue
					}
					if ws != cleanTarget && !strings.HasPrefix(cleanTarget, ws+string(filepath.Separator)) && !strings.HasPrefix(ws, cleanTarget+string(filepath.Separator)) {
						continue
					}
				}

				// Check Title first
				if matched, snip := matcher.FindMatch(c.Title, 140); matched {
					results = append(results, MatchResult{
						Type:        "conversation",
						Provider:    c.Provider,
						EntityID:    idutil.ShortID(c.ID),
						EntityTitle: c.Title,
						Workspace:   strings.TrimPrefix(c.WorkspaceDir, "file://"),
						Author:      "title",
						Timestamp:   c.UpdatedAt,
						Location:    "Title",
						Snippet:     snip,
					})
					if len(results) >= limit {
						return results, nil
					}
				}

				// Check Transcript raw bytes before loading detailed conversation turns
				if c.TranscriptPath != "" {
					raw, err := os.ReadFile(c.TranscriptPath)
					if err == nil && !matcher.QuickBytesMatch(raw) {
						// Transcript does not contain the match, skip loading turns
						continue
					}
				}

				// Check Conversation Turns
				detail, err := p.GetConversation(c.ID)
				if err != nil || detail == nil {
					continue
				}

				for _, t := range detail.Turns {
					if matched, snip := matcher.FindMatch(t.Content, 140); matched {
						results = append(results, MatchResult{
							Type:        "conversation",
							Provider:    c.Provider,
							EntityID:    idutil.ShortID(c.ID),
							EntityTitle: c.Title,
							Workspace:   strings.TrimPrefix(c.WorkspaceDir, "file://"),
							Author:      t.Role,
							Timestamp:   t.Timestamp,
							Location:    fmt.Sprintf("Step #%d", t.StepIndex),
							Snippet:     snip,
						})
						if len(results) >= limit {
							return results, nil
						}
					}
					// Also check thinking or toolcall if present
					if t.Thinking != "" {
						if matched, snip := matcher.FindMatch(t.Thinking, 140); matched {
							results = append(results, MatchResult{
								Type:        "conversation",
								Provider:    c.Provider,
								EntityID:    idutil.ShortID(c.ID),
								EntityTitle: c.Title,
								Workspace:   strings.TrimPrefix(c.WorkspaceDir, "file://"),
								Author:      "thinking",
								Timestamp:   t.Timestamp,
								Location:    fmt.Sprintf("Step #%d (thinking)", t.StepIndex),
								Snippet:     snip,
							})
							if len(results) >= limit {
								return results, nil
							}
						}
					}
				}
			}
		}
	}

	// 2. Search Memories (only if no workspace filter is applied, or if workspace matches memory source)
	if searchMemories && (opts.Workspace == "") {
		for _, p := range provList {
			mems, err := p.ListMemories()
			if err != nil {
				continue
			}
			for _, m := range mems {
				// Search title
				if matched, snip := matcher.FindMatch(m.Title, 140); matched {
					results = append(results, MatchResult{
						Type:        "memory",
						Provider:    m.Provider,
						EntityID:    idutil.ShortID(m.ID),
						EntityTitle: m.Title,
						Author:      "memory",
						Timestamp:   m.UpdatedAt,
						Location:    "Title",
						Snippet:     snip,
					})
					if len(results) >= limit {
						return results, nil
					}
					continue
				}

				// Search content
				if matched, snip := matcher.FindMatch(m.Content, 140); matched {
					results = append(results, MatchResult{
						Type:        "memory",
						Provider:    m.Provider,
						EntityID:    idutil.ShortID(m.ID),
						EntityTitle: m.Title,
						Author:      "memory",
						Timestamp:   m.UpdatedAt,
						Location:    "Content",
						Snippet:     snip,
					})
					if len(results) >= limit {
						return results, nil
					}
				}
			}
		}
	}

	// 3. Search Skills
	if searchSkills && (opts.Workspace == "") {
		for _, p := range provList {
			skills, err := p.ListSkills()
			if err != nil {
				continue
			}
			for _, s := range skills {
				// Check skill name and description
				if matched, snip := matcher.FindMatch(s.Name+" "+s.Description, 140); matched {
					results = append(results, MatchResult{
						Type:        "skill",
						Provider:    s.Provider,
						EntityID:    s.Name,
						EntityTitle: s.Name,
						Workspace:   s.Path,
						Author:      "skill",
						Timestamp:   time.Now(),
						Location:    "Description",
						Snippet:     snip,
					})
					if len(results) >= limit {
						return results, nil
					}
					continue
				}

				// Check skill instruction file (SKILL.md)
				skillMD := filepath.Join(s.Path, "SKILL.md")
				if data, err := os.ReadFile(skillMD); err == nil {
					if matched, snip := matcher.FindMatch(string(data), 140); matched {
						results = append(results, MatchResult{
							Type:        "skill",
							Provider:    s.Provider,
							EntityID:    s.Name,
							EntityTitle: s.Name,
							Workspace:   s.Path,
							Author:      "skill",
							Timestamp:   time.Now(),
							Location:    "SKILL.md",
							Snippet:     snip,
						})
						if len(results) >= limit {
							return results, nil
						}
					}
				}
			}
		}
	}

	return results, nil
}
