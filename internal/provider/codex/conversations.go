package codex

import (
	"agentcli.local/ai/internal/gitutil"
	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
	"agentcli.local/ai/internal/sqlite"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
		msgCount := 0
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
	summaries, err := p.ListConversations(provider.HistoryOptions{Search: id})
	if err != nil {
		return nil, err
	}
	var matched *provider.ConversationSummary
	for _, s := range summaries {
		if idutil.Match(id, s.ID) {
			matched = &s
			break
		}
	}
	if matched == nil {
		return nil, fmt.Errorf("codex conversation not found: %s", id)
	}

	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".codex/state_5.sqlite")

	// Get rollout path & artifacts from db
	cmd := exec.Command("sqlite3", dbPath, fmt.Sprintf("SELECT rollout_path FROM threads WHERE id = '%s';", matched.ID))
	var rolloutOut bytes.Buffer
	cmd.Stdout = &rolloutOut
	_ = cmd.Run()
	rolloutPath := strings.TrimSpace(rolloutOut.String())

	// Read turns from rollout JSONL
	turns, msgCount := readRolloutTurns(rolloutPath, 0)
	matched.MessagesCount = msgCount

	var initPrompt, lastResp string
	for _, t := range turns {
		if t.Role == "user" && initPrompt == "" {
			initPrompt = t.Content
		}
		if t.Role == "assistant" && strings.TrimSpace(t.Content) != "" {
			lastResp = t.Content
		}
	}
	if initPrompt == "" {
		initPrompt = matched.Title
	}

	// Fetch artifacts for thread
	var artifacts []provider.ArtifactInfo
	artCmd := exec.Command("sqlite3", "-separator", "\x1f", dbPath, fmt.Sprintf("SELECT id, artifact_type, identity_key, length(payload), created_at FROM thread_artifacts WHERE thread_id = '%s';", matched.ID))
	var artOut bytes.Buffer
	artCmd.Stdout = &artOut
	if err := artCmd.Run(); err == nil {
		sc := bufio.NewScanner(&artOut)
		for sc.Scan() {
			fields := strings.Split(sc.Text(), "\x1f")
			if len(fields) >= 5 {
				sz, _ := strconv.ParseInt(fields[3], 10, 64)
				created, _ := strconv.ParseInt(fields[4], 10, 64)
				artifacts = append(artifacts, provider.ArtifactInfo{
					Name:       fields[2],
					Type:       fields[1],
					SizeBytes:  sz,
					ModifiedAt: time.Unix(created, 0),
				})
			}
		}
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

func (p *CodexProvider) AuditConversation(id string) (*provider.AuditDossier, error) {
	var detail *provider.ConversationDetail
	var err error

	if id == "" {
		recent, err := p.ListConversations(provider.HistoryOptions{Last: true})
		if err != nil || len(recent) == 0 {
			return nil, fmt.Errorf("no recent Codex conversation found")
		}
		detail, err = p.GetConversation(recent[0].ID)
		if err != nil {
			return nil, err
		}
	} else {
		detail, err = p.GetConversation(id)
		if err != nil {
			return nil, err
		}
	}

	dossier := &provider.AuditDossier{
		Provider:           p.Name(),
		ConversationID:     detail.Summary.ID,
		Title:              detail.Summary.Title,
		LastActive:         detail.Summary.UpdatedAt,
		WorkspaceDir:       detail.Summary.WorkspaceDir,
		WorkspaceGit:       detail.WorkspaceGit,
		FullTranscriptPath: detail.TranscriptPath,
		TouchedArtifacts:   detail.Artifacts,
	}

	for _, turn := range detail.Turns {
		if turn.Role == "user" && dossier.UserPrompt == "" {
			dossier.UserPrompt = turn.Content
		}
		if turn.Role == "assistant" {
			dossier.AssistantSummary = turn.Content
		}
	}

	return dossier, nil
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
		if role == "developer" {
			// Skip internal role instructions
			continue
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
		if isHarnessError && contentStr == "" {
			contentStr = harnessErrStr
		}
		if contentStr == "" && thinkingStr == "" {
			continue
		}

		ts := time.Now()
		if obj.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, obj.Timestamp); err == nil {
				ts = t
			}
		}

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
