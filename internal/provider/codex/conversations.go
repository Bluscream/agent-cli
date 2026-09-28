package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentcli.local/ai/internal/gitutil"
	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
	"agentcli.local/ai/internal/sqlite"
)

func (p *CodexProvider) ListConversations(opts provider.HistoryOptions) ([]provider.ConversationSummary, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".codex/state_5.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nil
	}

	// Query threads from state_5.sqlite
	// columns: id, title, created_at, updated_at, cwd, model, rollout_path
	query := "SELECT id, title, created_at, updated_at, cwd, model, rollout_path FROM threads ORDER BY updated_at DESC;"
	var rows []struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		CreatedAt   int64  `json:"created_at"`
		UpdatedAt   int64  `json:"updated_at"`
		CWD         string `json:"cwd"`
		Model       string `json:"model"`
		RolloutPath string `json:"rollout_path"`
	}
	if err := sqlite.Query(dbPath, query, &rows); err != nil {
		return nil, err
	}
	results := []provider.ConversationSummary{}
	for _, row := range rows {
		id, title, cwd, model, rolloutPath := row.ID, row.Title, row.CWD, row.Model, row.RolloutPath
		createdSec, updatedSec := row.CreatedAt, row.UpdatedAt

		createdAt := time.Unix(createdSec, 0)
		updatedAt := time.Unix(updatedSec, 0)

		if !opts.Since.IsZero() && updatedAt.Before(opts.Since) {
			continue
		}
		if opts.Search != "" && !strings.Contains(strings.ToLower(title), strings.ToLower(opts.Search)) && !idutil.Match(opts.Search, id) {
			continue
		}

		var sizeBytes int64
		// The rollout file is only parsed by GetConversation, so the listing
		// cannot know the real figure.
		msgCount := provider.CountUnknown
		if fi, err := os.Stat(rolloutPath); err == nil {
			sizeBytes = fi.Size()
		}

		if title == "" {
			title = fmt.Sprintf("Codex %.8s", id)
		}

		results = append(results, provider.ConversationSummary{
			Provider:       p.Name(),
			ID:             id,
			Title:          title,
			MessagesCount:  msgCount,
			TotalSizeBytes: sizeBytes,
			CreatedAt:      createdAt,
			UpdatedAt:      updatedAt,
			WorkspaceDir:   cwd,
			Model:          model,
			TranscriptPath: rolloutPath,
		})
	}

	if opts.Last && len(results) > 0 {
		results = results[:1]
	} else if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	return results, nil
}

func (p *CodexProvider) GetConversation(id string) (*provider.ConversationDetail, error) {
	matched, err := provider.Find(p, id)
	if err != nil {
		return nil, err
	}

	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".codex/state_5.sqlite")

	// ListConversations already read rollout_path from this same table, so the
	// path is in hand; re-querying it discarded the error and turned a failed
	// query into a conversation that reported zero turns.
	rolloutPath := matched.TranscriptPath

	// Read turns from rollout JSONL
	turns, msgCount := readRolloutTurns(rolloutPath, 0)
	matched.MessagesCount = msgCount

	initPrompt, lastResp := provider.Endpoints(turns)
	if initPrompt == "" {
		initPrompt = matched.Title
	}

	artifacts, err := threadArtifacts(dbPath, matched.ID)
	if err != nil {
		return nil, err
	}
	matched.ArtifactsCount = len(artifacts)

	detail := &provider.ConversationDetail{
		Summary:        *matched,
		TranscriptPath: rolloutPath,
		WorkspaceGit:   gitutil.Inspect(matched.WorkspaceDir),
		Artifacts:      artifacts,
		Turns:          turns,
		InitialPrompt:  initPrompt,
		LastResponse:   lastResp,
	}

	return detail, nil
}

// threadArtifacts reads a thread's artifact rows as typed values rather than
// splitting a separator-delimited dump, which is the parsing internal/sqlite
// exists to replace.
func threadArtifacts(dbPath, threadID string) ([]provider.ArtifactInfo, error) {
	var rows []struct {
		Type        string `json:"artifact_type"`
		IdentityKey string `json:"identity_key"`
		SizeBytes   int64  `json:"size_bytes"`
		CreatedAt   int64  `json:"created_at"`
	}
	const q = `SELECT artifact_type, identity_key, length(payload) AS size_bytes, created_at
FROM thread_artifacts WHERE thread_id = ?;`
	if err := sqlite.Query(dbPath, q, &rows, threadID); err != nil {
		return nil, fmt.Errorf("read artifacts for thread %s: %w", threadID, err)
	}
	artifacts := make([]provider.ArtifactInfo, 0, len(rows))
	for _, r := range rows {
		artifacts = append(artifacts, provider.ArtifactInfo{
			Name:       r.IdentityKey,
			Type:       r.Type,
			SizeBytes:  r.SizeBytes,
			ModifiedAt: time.Unix(r.CreatedAt, 0),
		})
	}
	return artifacts, nil
}

func (p *CodexProvider) AuditConversation(id string) (*provider.AuditDossier, error) {
	return provider.Audit(p, id)
}

func (p *CodexProvider) RecoverConversations(dryRun bool) (string, error) {
	report := map[string]any{
		"provider": p.Name(),
		"note":     "Codex Desktop stores conversations in state_5.sqlite and rollout JSONLs under ~/.codex/sessions/",
		"status":   "healthy",
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	return string(data), nil
}

func readRolloutTurns(path string, limit int) ([]provider.TurnInfo, int) {
	var turns []provider.TurnInfo
	if path == "" {
		return turns, 0
	}
	f, err := os.Open(path)
	if err != nil {
		return turns, 0
	}
	defer f.Close()

	var all []provider.TurnInfo
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 5*1024*1024)

	step := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		step++

		var obj struct {
			Timestamp string `json:"timestamp"`
			Ordinal   int    `json:"ordinal"`
			Type      string `json:"type"`
			Payload   struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				Summary []struct {
					Text string `json:"text"`
				} `json:"summary"`
				Error any `json:"error"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}

		isHarnessError := false
		var harnessErrStr string
		if obj.Payload.Error != nil {
			isHarnessError = true
			if m, ok := obj.Payload.Error.(map[string]any); ok {
				if msg, ok := m["message"].(string); ok {
					harnessErrStr = fmt.Sprintf("[Harness Error: %s]", msg)
				}
			}
			if harnessErrStr == "" {
				b, _ := json.Marshal(obj.Payload.Error)
				harnessErrStr = fmt.Sprintf("[Harness Error: %s]", string(b))
			}
		}

		thinkingStr := ""
		if obj.Payload.Type == "reasoning" {
			var sb strings.Builder
			for _, s := range obj.Payload.Summary {
				if s.Text != "" {
					sb.WriteString(s.Text)
					sb.WriteString("\n")
				}
			}
			thinkingStr = strings.TrimSpace(sb.String())
		}

		if obj.Payload.Role == "" && len(obj.Payload.Content) == 0 && !isHarnessError && thinkingStr == "" {
			continue
		}

		role := obj.Payload.Role
		if obj.Payload.Type == "reasoning" {
			role = "assistant"
		}
		if role == "developer" {
			role = "system"
		}
		if isHarnessError && role == "" {
			role = "system"
		}

		var sb strings.Builder
		for _, c := range obj.Payload.Content {
			if c.Text != "" {
				if c.Type == "thought" {
					thinkingStr = c.Text
				} else {
					sb.WriteString(c.Text)
				}
			}
		}
		contentStr := strings.TrimSpace(sb.String())
		if role == "user" && (strings.HasPrefix(contentStr, "<recommended_plugins>") || strings.HasPrefix(contentStr, "<environment_context>")) {
			role = "system"
		}

		if isHarnessError && contentStr == "" {
			contentStr = harnessErrStr
		}
		if contentStr == "" && thinkingStr == "" {
			continue
		}

		// If this is pure reasoning without text content, attach it to the most recent assistant turn if available
		if contentStr == "" && thinkingStr != "" && len(all) > 0 && all[len(all)-1].Role == "assistant" {
			if all[len(all)-1].Thinking != "" {
				all[len(all)-1].Thinking += "\n" + thinkingStr
			} else {
				all[len(all)-1].Thinking = thinkingStr
			}
			continue
		}

		// Left as the zero time when the record carries none: reporting now()
		// would sort an undated turn ahead of everything real.
		ts := provider.ParseTimestamp(obj.Timestamp)

		all = append(all, provider.TurnInfo{
			StepIndex:      step,
			Role:           role,
			Content:        contentStr,
			Timestamp:      ts,
			Thinking:       thinkingStr,
			IsHarnessError: isHarnessError,
		})
	}

	if limit > 0 && len(all) > limit {
		turns = all[len(all)-limit:]
	} else {
		turns = all
	}

	return turns, step
}

// TranscriptRoots implements provider.TranscriptRooter.
func (p *CodexProvider) TranscriptRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return provider.ExistingDirs(filepath.Join(home, ".codex/sessions"))
}
