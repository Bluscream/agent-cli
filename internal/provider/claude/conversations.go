package claude

import (
	"agentcli.local/ai/internal/gitutil"
	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (p *ClaudeProvider) ListConversations(opts provider.HistoryOptions) ([]provider.ConversationSummary, error) {
	home, _ := os.UserHomeDir()
	projectsDir := filepath.Join(home, ".claude/projects")
	sessionsDir := filepath.Join(home, ".config/Claude/claude-code-sessions")

	convos := make(map[string]*provider.ConversationSummary)

	// 1. Scan ~/.claude/projects/**/*.jsonl
	_ = filepath.Walk(projectsDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}

		sessionID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		dirName := filepath.Base(filepath.Dir(path))
		wsDir := unescapeWorkspacePath(dirName)

		summary := parseClaudeTranscript(path, sessionID, wsDir, fi)
		convos[sessionID] = summary
		return nil
	})

	// 2. Scan ~/.config/Claude/claude-code-sessions/**/*.json
	_ = filepath.Walk(sessionsDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var doc struct {
			SessionID      string `json:"sessionId"`
			CLISessionID   string `json:"cliSessionId"`
			Title          string `json:"title"`
			CWD            string `json:"cwd"`
			OriginCWD      string `json:"originCwd"`
			CreatedAt      string `json:"createdAt"`
			LastActivityAt string `json:"lastActivityAt"`
			Model          string `json:"model"`
		}
		if err := json.Unmarshal(data, &doc); err == nil {
			id := doc.SessionID
			if id == "" {
				id = doc.CLISessionID
			}
			if id == "" {
				id = strings.TrimSuffix(filepath.Base(path), ".json")
			}

			c, exists := convos[id]
			if !exists {
				created := fi.ModTime()
				if doc.CreatedAt != "" {
					if t, err := time.Parse(time.RFC3339, doc.CreatedAt); err == nil {
						created = t
					}
				}
				updated := fi.ModTime()
				if doc.LastActivityAt != "" {
					if t, err := time.Parse(time.RFC3339, doc.LastActivityAt); err == nil {
						updated = t
					}
				}

				title := doc.Title
				if title == "" {
					title = fmt.Sprintf("Claude Session %.8s", id)
				}
				ws := doc.CWD
				if ws == "" {
					ws = doc.OriginCWD
				}

				convos[id] = &provider.ConversationSummary{
					Provider:       p.Name(),
					ID:             id,
					Title:          title,
					MessagesCount:  1,
					CreatedAt:      created,
					UpdatedAt:      updated,
					WorkspaceDir:   ws,
					Model:          doc.Model,
					TotalSizeBytes: fi.Size(),
				}
			} else {
				if doc.Title != "" {
					c.Title = doc.Title
				}
				if doc.Model != "" {
					c.Model = doc.Model
				}
			}
		}
		return nil
	})

	var results []provider.ConversationSummary
	for _, c := range convos {
		if !opts.Since.IsZero() && c.UpdatedAt.Before(opts.Since) {
			continue
		}
		if opts.Search != "" && !strings.Contains(strings.ToLower(c.Title), strings.ToLower(opts.Search)) && !idutil.Match(opts.Search, c.ID) {
			continue
		}
		results = append(results, *c)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].UpdatedAt.After(results[j].UpdatedAt)
	})

	if opts.Last && len(results) > 0 {
		results = results[:1]
	} else if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	return results, nil
}

func (p *ClaudeProvider) GetConversation(id string) (*provider.ConversationDetail, error) {
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
		return nil, fmt.Errorf("claude conversation not found: %s", id)
	}

	home, _ := os.UserHomeDir()
	projectsDir := filepath.Join(home, ".claude/projects")
	var trPath string
	_ = filepath.Walk(projectsDir, func(path string, fi os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(path, matched.ID+".jsonl") {
			trPath = path
			return filepath.SkipAll
		}
		return nil
	})

	turns := readClaudeTurns(trPath, 0)
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

	detail := &provider.ConversationDetail{
		Summary:        *matched,
		TranscriptPath: trPath,
		WorkspaceGit:   gitutil.Inspect(matched.WorkspaceDir),
		Turns:          turns,
		InitialPrompt:  initPrompt,
		LastResponse:   lastResp,
	}

	return detail, nil
}

func (p *ClaudeProvider) AuditConversation(id string) (*provider.AuditDossier, error) {
	var detail *provider.ConversationDetail
	var err error

	if id == "" {
		recent, err := p.ListConversations(provider.HistoryOptions{Last: true})
		if err != nil || len(recent) == 0 {
			return nil, fmt.Errorf("no recent Claude conversation found")
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
	}

	// Extract prompts & assistant messages
	for _, turn := range detail.Turns {
		if turn.Role == "user" && dossier.UserPrompt == "" {
			dossier.UserPrompt = turn.Content
		}
		if turn.Role == "assistant" {
			dossier.AssistantSummary = turn.Content
		}
	}

	if dossier.UserPrompt == "" && detail.Summary.Title != "" {
		dossier.UserPrompt = detail.Summary.Title
	}

	// Check if workspace contains tasks or plan
	if detail.WorkspaceGit.IsRepo {
		// Look for common task tracking files in workspace
		for _, name := range []string{"TODO.md", "task_list.md", "AGENTS.md"} {
			fp := filepath.Join(detail.WorkspaceGit.WorkDir, name)
			if data, err := os.ReadFile(fp); err == nil {
				dossier.PendingOpenTasks = extractPlanTasks(string(data))
				break
			}
		}
	}

	return dossier, nil
}

func (p *ClaudeProvider) RecoverConversations(dryRun bool) (string, error) {
	patchStatus := CheckPatchStatus()
	nudgeStatus := CheckNudgeStatus()
	report := map[string]any{
		"provider":     p.Name(),
		"patch_status": patchStatus,
		"nudge_status": nudgeStatus,
		"note":         "Claude session indices are maintained directly via JSONL transcripts in ~/.claude/projects/",
	}
	b, _ := json.MarshalIndent(report, "", "  ")
	return string(b), nil
}

func unescapeWorkspacePath(escaped string) string {
	// e.g. "-run-media-system-Data-Projects" -> "/run/media/system/Data/Projects"
	trimmed := strings.TrimPrefix(escaped, "-")
	return "/" + strings.ReplaceAll(trimmed, "-", "/")
}

func parseClaudeTranscript(path, sessionID, wsDir string, fi os.FileInfo) *provider.ConversationSummary {
	summary := &provider.ConversationSummary{
		Provider:       "claude",
		ID:             sessionID,
		Title:          fmt.Sprintf("Claude %.8s", sessionID),
		CreatedAt:      fi.ModTime(),
		UpdatedAt:      fi.ModTime(),
		WorkspaceDir:   wsDir,
		TotalSizeBytes: fi.Size(),
	}

	f, err := os.Open(path)
	if err != nil {
		return summary
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 5*1024*1024)

	stepCount := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		stepCount++

		var obj struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			Timestamp string `json:"timestamp"`
			CreatedAt string `json:"created_at"`
			Message   struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"message"`
			Content string `json:"content"`
			Text    string `json:"text"`
			CWD     string `json:"cwd"`
		}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}

		if obj.CWD != "" {
			summary.WorkspaceDir = obj.CWD
		}

		tsStr := obj.Timestamp
		if tsStr == "" {
			tsStr = obj.CreatedAt
		}
		if tsStr != "" {
			if t, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
				if summary.CreatedAt.IsZero() || t.Before(summary.CreatedAt) {
					summary.CreatedAt = t
				}
				if t.After(summary.UpdatedAt) {
					summary.UpdatedAt = t
				}
			}
		}

		isUser := (obj.Type == "user" || obj.Role == "user" || obj.Message.Role == "user")
		if isUser && summary.Title == fmt.Sprintf("Claude %.8s", sessionID) {
			contentStr := obj.Content
			if contentStr == "" {
				contentStr = obj.Text
			}
			if contentStr == "" {
				switch v := obj.Message.Content.(type) {
				case string:
					contentStr = v
				case []any:
					for _, item := range v {
						if m, ok := item.(map[string]any); ok && m["type"] == "text" {
							if txt, ok := m["text"].(string); ok {
								contentStr = txt
								break
							}
						}
					}
				}
			}
			title := cleanTitleText(contentStr)
			if title != "" {
				summary.Title = title
			}
		}

		if summary.Title != fmt.Sprintf("Claude %.8s", sessionID) && summary.WorkspaceDir != "" && stepCount >= 10 {
			break
		}
		if stepCount > 40 {
			break
		}
	}
	summary.MessagesCount = stepCount
	return summary
}

func readClaudeTurns(path string, limit int) []provider.TurnInfo {
	var turns []provider.TurnInfo
	if path == "" {
		return turns
	}
	f, err := os.Open(path)
	if err != nil {
		return turns
	}
	defer f.Close()

	var all []provider.TurnInfo
	var firstUserTurn *provider.TurnInfo
	r := bufio.NewReaderSize(f, 64*1024)
	var lineBuf bytes.Buffer
	step := 0

	for {
		part, isPrefix, err := r.ReadLine()
		if len(part) > 0 {
			lineBuf.Write(part)
		}
		if err != nil {
			break
		}
		if isPrefix {
			continue
		}

		line := bytes.TrimSpace(lineBuf.Bytes())
		lineBuf.Reset()
		if len(line) == 0 {
			continue
		}
		step++

		var obj struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			Timestamp string `json:"timestamp"`
			CreatedAt string `json:"created_at"`
			Message   struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"message"`
			Content string `json:"content"`
			Text    string `json:"text"`
			Subtype string `json:"subtype"`
			Level   string `json:"level"`
			Error   any    `json:"error"`
		}
		if err := json.Unmarshal(line, &obj); err != nil {
			continue
		}

		isHarnessError := false
		if obj.Type == "system" && (obj.Subtype == "api_error" || obj.Level == "error") {
			isHarnessError = true
		}

		role := "assistant"
		if obj.Type == "user" || obj.Role == "user" || obj.Message.Role == "user" {
			role = "user"
		} else if isHarnessError {
			role = "system"
		} else if obj.Type != "assistant" && obj.Type != "model" && obj.Role != "assistant" && obj.Message.Role != "assistant" {
			continue
		}

		contentStr := obj.Content
		if contentStr == "" {
			contentStr = obj.Text
		}
		thinkingStr := ""
		if contentStr == "" {
			switch v := obj.Message.Content.(type) {
			case string:
				contentStr = v
			case []any:
				var sb strings.Builder
				var tb strings.Builder
				for _, item := range v {
					if m, ok := item.(map[string]any); ok {
						switch m["type"] {
						case "text":
							sb.WriteString(fmt.Sprintf("%v\n", m["text"]))
						case "tool_use":
							sb.WriteString(fmt.Sprintf("[Tool Call: %v]\n", m["name"]))
						case "thinking":
							if th, ok := m["thinking"].(string); ok {
								tb.WriteString(th)
								tb.WriteString("\n")
							}
						}
					}
				}
				contentStr = sb.String()
				thinkingStr = strings.TrimSpace(tb.String())
			}
		}

		contentStr = strings.TrimSpace(contentStr)

		if role == "user" && (strings.HasPrefix(contentStr, "<local-command-caveat>") || strings.HasPrefix(contentStr, "<command-name>") || strings.HasPrefix(contentStr, "<local-command-stdout>")) {
			role = "system"
		}

		if isHarnessError && contentStr == "" {
			if b, err := json.Marshal(obj.Error); err == nil {
				contentStr = fmt.Sprintf("[Harness Error: %s]", string(b))
			} else {
				contentStr = fmt.Sprintf("[Harness Error: %v]", obj.Error)
			}
		}

		contentStr = strings.TrimSpace(contentStr)
		if contentStr == "" && thinkingStr == "" {
			continue
		}

		ts := time.Now()
		tsStr := obj.Timestamp
		if tsStr == "" {
			tsStr = obj.CreatedAt
		}
		if tsStr != "" {
			if t, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
				ts = t
			}
		}

		turn := provider.TurnInfo{
			StepIndex:      step,
			Role:           role,
			Content:        contentStr,
			Timestamp:      ts,
			Thinking:       thinkingStr,
			IsHarnessError: isHarnessError,
		}
		if role == "user" && firstUserTurn == nil {
			firstUserTurn = &turn
		}
		all = append(all, turn)
	}

	if limit > 0 && len(all) > limit {
		turns = all[len(all)-limit:]
		if firstUserTurn != nil && (len(turns) == 0 || turns[0].StepIndex != firstUserTurn.StepIndex) {
			turns = append([]provider.TurnInfo{*firstUserTurn}, turns...)
		}
	} else {
		turns = all
	}

	return turns
}

func cleanTitleText(text string) string {
	lines := strings.Split(text, "\n")
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t != "" && !strings.HasPrefix(t, "file://") {
			if len(t) > 80 {
				t = t[:80]
			}
			return t
		}
	}
	return ""
}

func extractPlanTasks(md string) []string {
	var tasks []string
	lines := strings.Split(md, "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "- [ ]") || strings.HasPrefix(trimmed, "* [ ]") {
			tasks = append(tasks, strings.TrimSpace(trimmed[5:]))
		}
	}
	return tasks
}
