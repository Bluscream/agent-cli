package antigravity

import (
	"agentcli.local/ai/internal/gitutil"
	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (p *AntigravityProvider) ListConversations(opts provider.HistoryOptions) ([]provider.ConversationSummary, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".config/Antigravity IDE/User/globalStorage/state.vscdb")
	brainDir := filepath.Join(home, ".gemini/antigravity-ide/brain")
	convDir := filepath.Join(home, ".gemini/antigravity-ide/conversations")

	// Read state.vscdb trajectory summaries
	convoMap := make(map[string]*provider.ConversationSummary)

	queryCmd := exec.Command("sqlite3", dbPath, "SELECT value FROM ItemTable WHERE key = 'antigravityUnifiedStateSync.trajectorySummaries';")
	var valOut bytes.Buffer
	queryCmd.Stdout = &valOut
	if err := queryCmd.Run(); err == nil {
		rawB64 := strings.TrimSpace(valOut.String())
		if rawBytes, err := base64.StdEncoding.DecodeString(rawB64); err == nil {
			entries, _ := extractMapEntries(rawBytes)
			for cid, sub := range entries {
				s := parseTrajectorySummary(cid, sub)
				convoMap[cid] = s
			}
		}
	}

	// Also discover sessions from brainDir
	if entries, err := os.ReadDir(brainDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			cid := e.Name()
			if len(cid) != 36 || strings.Count(cid, "-") != 4 {
				continue
			}

			c, exists := convoMap[cid]
			if !exists {
				meta := extractConvoInfo(cid, convDir, brainDir)
				c = &provider.ConversationSummary{
					Provider:      p.Name(),
					ID:            cid,
					Title:         meta.Title,
					MessagesCount: meta.StepCount,
					CreatedAt:     time.Unix(meta.CreatedTS, 0),
					UpdatedAt:     time.Unix(meta.ModifiedTS, 0),
					WorkspaceDir:  meta.WorkspaceURI,
				}
				convoMap[cid] = c
			}

			// Compute artifacts count & total disk size
			artCount, sizeBytes := inspectBrainDir(filepath.Join(brainDir, cid))
			c.ArtifactsCount = artCount
			c.TotalSizeBytes = sizeBytes

			// Check transcript.jsonl for newer activity than SQLite state.vscdb sync
			tr := filepath.Join(brainDir, cid, ".system_generated", "logs", "transcript.jsonl")
			if fi, err := os.Stat(tr); err == nil {
				if fi.ModTime().After(c.UpdatedAt) {
					c.UpdatedAt = fi.ModTime()
				}
			} else if !exists && artCount == 0 {
				// If this was an unindexed brain dir with 0 artifacts and no transcript, skip ghost
				delete(convoMap, cid)
			}
		}
	}

	var results []provider.ConversationSummary
	for _, c := range convoMap {
		if !opts.Since.IsZero() && c.UpdatedAt.Before(opts.Since) {
			continue
		}
		if opts.Search != "" && !strings.Contains(strings.ToLower(c.Title), strings.ToLower(opts.Search)) && !idutil.Match(opts.Search, c.ID) {
			continue
		}
		c.Title = cleanTitleText(c.Title)
		results = append(results, *c)
	}

	// Sort descending by UpdatedAt
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

func (p *AntigravityProvider) GetConversation(id string) (*provider.ConversationDetail, error) {
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
		return nil, fmt.Errorf("conversation not found: %s", id)
	}

	home, _ := os.UserHomeDir()
	brainPath := filepath.Join(home, ".gemini/antigravity-ide/brain", matched.ID)
	trPath := filepath.Join(brainPath, ".system_generated", "logs", "transcript.jsonl")

	turns := readTurns(trPath, 0) // read all turns
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
		Artifacts:      listArtifacts(brainPath),
		WorkspaceGit:   gitutil.Inspect(matched.WorkspaceDir),
		Turns:          turns,
		InitialPrompt:  initPrompt,
		LastResponse:   lastResp,
	}

	return detail, nil
}

func (p *AntigravityProvider) AuditConversation(id string) (*provider.AuditDossier, error) {
	var detail *provider.ConversationDetail
	var err error
	if id == "" {
		// Last conversation
		recent, err := p.ListConversations(provider.HistoryOptions{Last: true})
		if err != nil || len(recent) == 0 {
			return nil, fmt.Errorf("no recent Antigravity conversation found")
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

	// Extract prompt & assistant summary from turns
	for _, turn := range detail.Turns {
		if turn.Role == "user" && dossier.UserPrompt == "" {
			dossier.UserPrompt = turn.Content
		}
		if turn.Role == "assistant" {
			dossier.AssistantSummary = turn.Content
		}
	}

	// Look for implementation_plan.md or walkthrough.md
	for _, art := range detail.Artifacts {
		if art.Name == "implementation_plan.md" {
			if content, err := os.ReadFile(art.Path); err == nil {
				dossier.PendingOpenTasks = extractPlanTasks(string(content))
			}
		}
	}

	return dossier, nil
}

func (p *AntigravityProvider) RecoverConversations(dryRun bool) (string, error) {
	report, err := Recover(dryRun, true)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	return string(data), nil
}

func inspectBrainDir(dir string) (int, int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	var count int
	var totalSize int64
	for _, e := range entries {
		if e.Name() == ".system_generated" {
			continue
		}
		if !e.IsDir() {
			count++
			if fi, err := e.Info(); err == nil {
				totalSize += fi.Size()
			}
		}
	}
	return count, totalSize
}

func listArtifacts(brainDir string) []provider.ArtifactInfo {
	var artifacts []provider.ArtifactInfo
	_ = filepath.Walk(brainDir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		if strings.Contains(p, ".system_generated") {
			return nil
		}
		rel, _ := filepath.Rel(brainDir, p)
		ext := filepath.Ext(p)
		artifacts = append(artifacts, provider.ArtifactInfo{
			Path:       p,
			Name:       rel,
			SizeBytes:  fi.Size(),
			Type:       strings.TrimPrefix(ext, "."),
			ModifiedAt: fi.ModTime(),
		})
		return nil
	})
	return artifacts
}

func readTurns(trPath string, limit int) []provider.TurnInfo {
	var turns []provider.TurnInfo
	f, err := os.Open(trPath)
	if err != nil {
		return turns
	}
	defer f.Close()

	var all []provider.TurnInfo
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		var d struct {
			StepIndex int    `json:"step_index"`
			Type      string `json:"type"`
			Content   any    `json:"content"`
			CreatedAt string `json:"created_at"`
			Thinking  string `json:"thinking"`
			ToolCalls []struct {
				Name string `json:"name"`
			} `json:"tool_calls"`
		}
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			continue
		}

		role := "system"
		if d.Type == "USER_INPUT" {
			role = "user"
		} else if d.Type == "PLANNER_RESPONSE" || d.Type == "MODEL" {
			role = "assistant"
		}

		contentStr := ""
		switch v := d.Content.(type) {
		case string:
			contentStr = v
		default:
			b, _ := json.Marshal(v)
			contentStr = string(b)
		}

		if strings.Contains(contentStr, "<USER_REQUEST>") {
			parts := strings.Split(contentStr, "<USER_REQUEST>")
			if len(parts) > 1 {
				contentStr = strings.Split(parts[1], "</USER_REQUEST>")[0]
			}
		}

		ts := time.Now()
		if d.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, d.CreatedAt); err == nil {
				ts = t
			}
		}

		tc := ""
		if len(d.ToolCalls) > 0 {
			tc = d.ToolCalls[0].Name
		}

		all = append(all, provider.TurnInfo{
			StepIndex: d.StepIndex,
			Role:      role,
			Content:   strings.TrimSpace(contentStr),
			Timestamp: ts,
			ToolCall:  tc,
			Thinking:  strings.TrimSpace(d.Thinking),
		})
	}

	if limit > 0 && len(all) > limit {
		turns = all[len(all)-limit:]
	} else {
		turns = all
	}

	return turns
}

func parseTrajectorySummary(cid string, rawSub []byte) *provider.ConversationSummary {
	s := &provider.ConversationSummary{
		Provider:  "antigravity",
		ID:        cid,
		Title:     fmt.Sprintf("Conversation %.8s", cid),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	subFields := parseMsg(rawSub)
	for _, sf := range subFields {
		if sf.fieldNum == 2 && sf.wireType == 2 {
			valFields := parseMsg(sf.data)
			for _, vf := range valFields {
				if vf.fieldNum == 1 && vf.wireType == 2 {
					innerBytes, err := base64.StdEncoding.DecodeString(string(vf.data))
					if err == nil {
						innerFields := parseMsg(innerBytes)
						for _, inf := range innerFields {
							switch inf.fieldNum {
							case 1:
								s.Title = string(inf.data)
							case 2:
								s.MessagesCount = int(inf.varint)
							case 3:
								tsFields := parseMsg(inf.data)
								for _, tf := range tsFields {
									if tf.fieldNum == 1 {
										s.UpdatedAt = time.Unix(int64(tf.varint), 0)
									}
								}
							case 7:
								tsFields := parseMsg(inf.data)
								for _, tf := range tsFields {
									if tf.fieldNum == 1 {
										s.CreatedAt = time.Unix(int64(tf.varint), 0)
									}
								}
							case 9:
								wsFields := parseMsg(inf.data)
								for _, wf := range wsFields {
									if wf.fieldNum == 1 {
										s.WorkspaceDir = string(wf.data)
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return s
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
