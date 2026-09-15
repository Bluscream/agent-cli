package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentcli.local/ai/internal/gitutil"
	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
)

type ClaudeProvider struct{}

func New() *ClaudeProvider {
	return &ClaudeProvider{}
}

func init() {
	provider.Register(New())
}

func (p *ClaudeProvider) Name() string {
	return "claude"
}

func (p *ClaudeProvider) DisplayName() string {
	return "Claude Desktop"
}

func (p *ClaudeProvider) Status() (provider.ProviderInfo, error) {
	home, _ := os.UserHomeDir()
	appImage := filepath.Join(home, ".local/bin/Claude_Desktop.AppImage")
	configPath := filepath.Join(home, ".config/Claude/claude_desktop_config.json")
	dataPath := filepath.Join(home, ".config/Claude")

	installed := false
	if _, err := os.Stat(appImage); err == nil {
		installed = true
	}

	metrics := provider.FindProcesses("claude_desktop", "claude-desktop")

	info := provider.ProviderInfo{
		ID:             p.Name(),
		DisplayName:    p.DisplayName(),
		Origin:         "io.github.aaddrick.claude-desktop-debian (GitHub)",
		Installed:      installed,
		BinaryPath:     appImage,
		ConfigPath:     configPath,
		DataPath:       dataPath,
		Running:        metrics.Running,
		PIDs:           metrics.PIDs,
		CPUPercent:     metrics.CPUPercent,
		MemoryRSSBytes: metrics.MemoryRSS,
	}

	// Count MCP servers
	if data, err := os.ReadFile(configPath); err == nil {
		var doc struct {
			MCPServers map[string]any `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &doc); err == nil {
			info.MCPCount = len(doc.MCPServers)
		}
	}

	// Determine if busy (activity in any claude project transcript within 20s)
	projectsDir := filepath.Join(home, ".claude/projects")
	_ = filepath.Walk(projectsDir, func(path string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.HasSuffix(path, ".jsonl") {
			if provider.IsFileRecentlyActive(path, 25*time.Second) {
				info.Busy = true
				info.ActiveTask = fmt.Sprintf("Active transcript in %s", filepath.Base(path))
				return filepath.SkipAll
			}
		}
		return nil
	})

	return info, nil
}

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
					title = fmt.Sprintf("Claude Session %s", id[:8])
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

func (p *ClaudeProvider) ListMemories() ([]provider.MemoryItem, error) {
	home, _ := os.UserHomeDir()
	var items []provider.MemoryItem

	addMD := func(path, id, title string) {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		fi, _ := os.Stat(path)
		modTime := time.Now()
		if fi != nil {
			modTime = fi.ModTime()
		}
		items = append(items, provider.MemoryItem{
			Provider:   p.Name(),
			ID:         id,
			Title:      title,
			Content:    string(data),
			SourceFile: path,
			CreatedAt:  modTime,
			UpdatedAt:  modTime,
		})
	}

	// 1. Global memory file: ~/.claude/CLAUDE.md
	addMD(
		filepath.Join(home, ".claude/CLAUDE.md"),
		"global-claude-md",
		"Global Claude Instructions (~/.claude/CLAUDE.md)",
	)

	// 2. Per-project memory files: ~/.claude/projects/<slug>/memory/*.md
	// The slug is the workspace path with slashes replaced by dashes.
	projectsDir := filepath.Join(home, ".claude/projects")
	projEntries, err := os.ReadDir(projectsDir)
	if err == nil {
		for _, proj := range projEntries {
			if !proj.IsDir() {
				continue
			}
			memDir := filepath.Join(projectsDir, proj.Name(), "memory")
			memEntries, err := os.ReadDir(memDir)
			if err != nil {
				continue
			}
			// Convert slug back to a readable workspace label.
			workspaceLabel := strings.ReplaceAll(proj.Name(), "-", "/")
			for _, mem := range memEntries {
				if mem.IsDir() || !strings.HasSuffix(mem.Name(), ".md") {
					continue
				}
				memPath := filepath.Join(memDir, mem.Name())
				id := "project-" + proj.Name() + "-" + strings.TrimSuffix(mem.Name(), ".md")
				title := fmt.Sprintf("%s (%s)", strings.TrimSuffix(mem.Name(), ".md"), workspaceLabel)
				addMD(memPath, id, title)
			}
		}
	}

	return items, nil
}

func (p *ClaudeProvider) BackupMemories(destDir string) (string, error) {
	items, err := p.ListMemories()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}
	destFile := filepath.Join(destDir, fmt.Sprintf("claude-memories-%d.json", time.Now().Unix()))
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(destFile, data, 0644); err != nil {
		return "", err
	}
	return destFile, nil
}

func (p *ClaudeProvider) PurgeMemories() (int, error) {
	items, err := p.ListMemories()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, it := range items {
		if it.SourceFile != "" {
			if err := os.Remove(it.SourceFile); err == nil {
				count++
			}
		}
	}
	return count, nil
}

func (p *ClaudeProvider) ListSkills() ([]provider.SkillItem, error) {
	home, _ := os.UserHomeDir()
	searchRoots := []string{
		filepath.Join(home, ".config/Claude/local-agent-mode-sessions"),
		filepath.Join(home, ".claude/skills"),
		filepath.Join(home, ".config/Claude/plugins"),
	}

	var skills []provider.SkillItem
	seen := make(map[string]bool)

	for _, root := range searchRoots {
		_ = filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
			if err != nil || !fi.IsDir() {
				return nil
			}
			skillMD := filepath.Join(path, "SKILL.md")
			if _, err := os.Stat(skillMD); err == nil {
				name := filepath.Base(path)
				if seen[name] {
					return nil
				}
				seen[name] = true

				desc := ""
				if content, err := os.ReadFile(skillMD); err == nil {
					desc = extractSkillDescription(string(content))
				}

				skills = append(skills, provider.SkillItem{
					Provider:    p.Name(),
					Name:        name,
					Description: desc,
					Path:        path,
					IsBuiltin:   strings.Contains(path, "local-agent-mode-sessions"),
				})
			}
			return nil
		})
	}

	return skills, nil
}

func (p *ClaudeProvider) ImportMemory(item provider.MemoryItem) error {
	home, _ := os.UserHomeDir()
	slug := idutil.ShortID(item.ID)
	if slug == "" {
		slug = fmt.Sprintf("imported-%d", time.Now().Unix())
	}
	memDir := filepath.Join(home, ".claude/projects/-imported-shared/memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		return err
	}
	destFile := filepath.Join(memDir, slug+".md")
	content := fmt.Sprintf("# %s\n\n%s\n", item.Title, item.Content)
	return os.WriteFile(destFile, []byte(content), 0644)
}

func (p *ClaudeProvider) ImportSkill(sourceDir string) error {
	home, _ := os.UserHomeDir()
	skillName := filepath.Base(sourceDir)
	destDir := filepath.Join(home, ".claude/skills", skillName)
	return copyDir(sourceDir, destDir)
}

func (p *ClaudeProvider) PurgeSkills() (int, error) {
	home, _ := os.UserHomeDir()
	skillsDir := filepath.Join(home, ".claude/skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return 0, nil
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			if err := os.RemoveAll(filepath.Join(skillsDir, e.Name())); err == nil {
				count++
			}
		}
	}
	return count, nil
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return err
			}
			if err := os.WriteFile(dstPath, data, 0644); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *ClaudeProvider) ListPlugins() ([]provider.PluginItem, error) {
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".config/Claude/plugins")
	var list []provider.PluginItem

	// Check loader hook status
	pst := CheckPatchStatus()
	hookStatus := "unpatched"
	if pst.IsPatched {
		hookStatus = "active"
	}
	list = append(list, provider.PluginItem{
		Provider:    p.Name(),
		Name:        "claude-asar-hook",
		Type:        "ASAR Hook",
		Path:        pst.AppImagePath,
		Status:      hookStatus,
		Description: "Electron main-process injector hook for Claude Desktop AppImage",
	})

	entries, err := os.ReadDir(pluginsDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			status := "installed"
			if name == "loader.js" {
				status = "loader"
			}
			list = append(list, provider.PluginItem{
				Provider:    p.Name(),
				Name:        name,
				Type:        "Web Script / Plugin",
				Path:        filepath.Join(pluginsDir, name),
				Status:      status,
				Description: fmt.Sprintf("JavaScript plugin injected into Claude Desktop WebContents (%s)", name),
			})
		}
	}
	return list, nil
}

func (p *ClaudeProvider) InstallPlugin(sourcePath string) error {
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".config/Claude/plugins")
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		return err
	}

	fi, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	destPath := filepath.Join(pluginsDir, filepath.Base(sourcePath))
	if fi.IsDir() {
		return copyDir(sourcePath, destPath)
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	return os.WriteFile(destPath, data, 0644)
}

func (p *ClaudeProvider) UninstallPlugin(name string) error {
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".config/Claude/plugins")
	target := filepath.Join(pluginsDir, name)
	if _, err := os.Stat(target); err != nil {
		// try with .js
		target = filepath.Join(pluginsDir, name+".js")
	}
	return os.RemoveAll(target)
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

// Helpers

func unescapeWorkspacePath(escaped string) string {
	// e.g. "-run-media-system-Data-Projects" -> "/run/media/system/Data/Projects"
	trimmed := strings.TrimPrefix(escaped, "-")
	return "/" + strings.ReplaceAll(trimmed, "-", "/")
}

func parseClaudeTranscript(path, sessionID, wsDir string, fi os.FileInfo) *provider.ConversationSummary {
	summary := &provider.ConversationSummary{
		Provider:       "claude",
		ID:             sessionID,
		Title:          fmt.Sprintf("Claude %s", sessionID[:8]),
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
		if isUser && summary.Title == fmt.Sprintf("Claude %s", sessionID[:8]) {
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

		if summary.Title != fmt.Sprintf("Claude %s", sessionID[:8]) && summary.WorkspaceDir != "" && stepCount >= 10 {
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
		}
		if err := json.Unmarshal(line, &obj); err != nil {
			continue
		}

		role := "assistant"
		if obj.Type == "user" || obj.Role == "user" || obj.Message.Role == "user" {
			role = "user"
		} else if obj.Type != "assistant" && obj.Type != "model" && obj.Role != "assistant" && obj.Message.Role != "assistant" {
			continue
		}

		contentStr := obj.Content
		if contentStr == "" {
			contentStr = obj.Text
		}
		if contentStr == "" {
			switch v := obj.Message.Content.(type) {
			case string:
				contentStr = v
			case []any:
				var sb strings.Builder
				for _, item := range v {
					if m, ok := item.(map[string]any); ok {
						if m["type"] == "text" {
							sb.WriteString(fmt.Sprintf("%v\n", m["text"]))
						} else if m["type"] == "tool_use" {
							sb.WriteString(fmt.Sprintf("[Tool Call: %v]\n", m["name"]))
						}
					}
				}
				contentStr = sb.String()
			}
		}

		contentStr = strings.TrimSpace(contentStr)
		if contentStr == "" {
			continue
		}

		if len(contentStr) > 600 {
			contentStr = contentStr[:600] + "..."
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
			StepIndex: step,
			Role:      role,
			Content:   contentStr,
			Timestamp: ts,
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

func extractSkillDescription(content string) string {
	lines := strings.Split(content, "\n")
	inFrontmatter := false
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "---" {
			if inFrontmatter {
				break
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

// GetLimits reads Claude's plan-usage-history.json and buddy-tokens.json to
// surface quota consumption without requiring an internet call.
//
//   - fh  = "fast" (Opus-class) message quota remaining as 0–100%. 0 = exhausted.
//   - sd  = "standard/daily" message quota remaining as 0–100%. 0 = exhausted.
//
// The window is 5 hours for fh and resets at midnight (UTC) for sd, but
// Claude does not store the exact reset time locally, so we approximate.
func (p *ClaudeProvider) GetLimits() ([]provider.LimitInfo, error) {
	home, _ := os.UserHomeDir()
	var limits []provider.LimitInfo
	now := time.Now()

	// --- plan-usage-history.json ---
	usagePath := filepath.Join(home, ".config/Claude/plan-usage-history.json")
	if data, err := os.ReadFile(usagePath); err == nil {
		var doc struct {
			Samples []struct {
				T   int64  `json:"t"` // Unix ms
				Org string `json:"org"`
				U   struct {
					FH int `json:"fh"` // fast/high-speed quota remaining %
					SD int `json:"sd"` // standard/daily remaining %
				} `json:"u"`
			} `json:"samples"`
		}
		if json.Unmarshal(data, &doc) == nil && len(doc.Samples) > 0 {
			last := doc.Samples[len(doc.Samples)-1]
			// fh: 5-hour rolling window; approximate next refill
			fhUsedPct := float64(100 - last.U.FH)
			fhRefill := time.Now().Add(5 * time.Hour).Truncate(5 * time.Hour)
			limits = append(limits, provider.LimitInfo{
				Provider:    p.Name(),
				Name:        "Fast messages (Opus-class)",
				Used:        -1,
				Limit:       -1,
				UsedPct:     fhUsedPct,
				Unit:        "%",
				RefillAt:    fhRefill,
				RefillEvery: "5 h rolling",
				Note:        fmt.Sprintf("%d%% remaining", last.U.FH),
			})
			// sd: daily reset at midnight UTC
			sdUsedPct := float64(100 - last.U.SD)
			nextMidnight := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day()+1, 0, 0, 0, 0, time.UTC)
			limits = append(limits, provider.LimitInfo{
				Provider:    p.Name(),
				Name:        "Standard messages (daily)",
				Used:        -1,
				Limit:       -1,
				UsedPct:     sdUsedPct,
				Unit:        "%",
				RefillAt:    nextMidnight,
				RefillEvery: "daily (midnight UTC)",
				Note:        fmt.Sprintf("%d%% remaining", last.U.SD),
			})
		}
	}

	// --- buddy-tokens.json ---
	tokPath := filepath.Join(home, ".config/Claude/buddy-tokens.json")
	if data, err := os.ReadFile(tokPath); err == nil {
		var doc struct {
			TokensToday struct {
				Date   string `json:"date"`
				Tokens int64  `json:"tokens"`
			} `json:"tokens-today"`
		}
		if json.Unmarshal(data, &doc) == nil && doc.TokensToday.Tokens > 0 {
			limits = append(limits, provider.LimitInfo{
				Provider:    p.Name(),
				Name:        "Tokens sent today",
				Used:        doc.TokensToday.Tokens,
				Limit:       -1,
				UsedPct:     -1,
				Unit:        "tokens",
				RefillAt:    time.Time{},
				RefillEvery: "daily",
				Note:        doc.TokensToday.Date,
			})
		}
	}

	return limits, nil
}

// GetActiveAccount returns the logged-in Claude account discovered from ~/.claude.json
// or ~/.config/Claude/config.json.
func (p *ClaudeProvider) GetActiveAccount() (*provider.AccountInfo, error) {
	home, _ := os.UserHomeDir()
	claudeJSONPath := filepath.Join(home, ".claude.json")

	if data, err := os.ReadFile(claudeJSONPath); err == nil {
		var doc struct {
			OAuthAccount struct {
				AccountUUID      string `json:"accountUuid"`
				EmailAddress     string `json:"emailAddress"`
				DisplayName      string `json:"displayName"`
				OrganizationType string `json:"organizationType"`
				BillingType      string `json:"billingType"`
			} `json:"oauthAccount"`
		}
		if json.Unmarshal(data, &doc) == nil && (doc.OAuthAccount.AccountUUID != "" || doc.OAuthAccount.EmailAddress != "") {
			disp := doc.OAuthAccount.DisplayName
			if disp == "" {
				disp = doc.OAuthAccount.EmailAddress
			}
			plan := doc.OAuthAccount.OrganizationType
			if plan == "claude_pro" {
				plan = "Claude Pro"
			} else if plan == "" {
				plan = doc.OAuthAccount.BillingType
			}
			return &provider.AccountInfo{
				Provider:    p.Name(),
				ID:          doc.OAuthAccount.AccountUUID,
				DisplayName: disp,
				Email:       doc.OAuthAccount.EmailAddress,
				Plan:        plan,
				IsActive:    true,
				ActiveIn:    "Claude Desktop",
				ConfigPath:  claudeJSONPath,
			}, nil
		}
	}

	// Fallback to config.json
	cfgPath := filepath.Join(home, ".config/Claude/config.json")
	if data, err := os.ReadFile(cfgPath); err == nil {
		var doc struct {
			LastKnownAccountUUID string `json:"lastKnownAccountUuid"`
		}
		if json.Unmarshal(data, &doc) == nil && doc.LastKnownAccountUUID != "" {
			return &provider.AccountInfo{
				Provider:    p.Name(),
				ID:          doc.LastKnownAccountUUID,
				DisplayName: idutil.ShortID(doc.LastKnownAccountUUID),
				Email:       "-",
				Plan:        "Claude Desktop",
				IsActive:    true,
				ActiveIn:    "Claude Desktop",
				ConfigPath:  cfgPath,
			}, nil
		}
	}

	return nil, fmt.Errorf("no active Claude account found")
}

// GetAccounts returns all accounts known to Claude.
func (p *ClaudeProvider) GetAccounts() ([]provider.AccountInfo, error) {
	active, err := p.GetActiveAccount()
	if err != nil || active == nil {
		return nil, nil
	}
	return []provider.AccountInfo{*active}, nil
}

// GetModels reads available Claude models dynamically from local ~/.claude.json caches
// and recent session logs. No models are hardcoded. If no models are found locally,
// an empty list is returned.
func (p *ClaudeProvider) GetModels() ([]provider.ModelInfo, error) {
	home, _ := os.UserHomeDir()
	claudeJSONPath := filepath.Join(home, ".claude.json")

	// Determine active model from most recently modified session
	activeModel := ""
	var latestTime time.Time
	sessionsDir := filepath.Join(home, ".config/Claude/claude-code-sessions")
	_ = filepath.Walk(sessionsDir, func(path string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.HasSuffix(path, ".json") {
			if data, err := os.ReadFile(path); err == nil {
				var doc struct {
					Model string `json:"model"`
				}
				if json.Unmarshal(data, &doc) == nil && doc.Model != "" {
					if fi.ModTime().After(latestTime) {
						latestTime = fi.ModTime()
						activeModel = doc.Model
					}
				}
			}
		}
		return nil
	})

	type discoveredModel struct {
		id          string
		displayName string
		description string
	}
	modelMap := make(map[string]discoveredModel)

	data, err := os.ReadFile(claudeJSONPath)
	if err == nil {
		var doc struct {
			AdditionalModelOptions []struct {
				Value       string `json:"value"`
				Label       string `json:"label"`
				Description string `json:"description"`
				Disabled    bool   `json:"disabled"`
			} `json:"additionalModelOptionsCache"`
			GrowthBookFeatures map[string]any `json:"cachedGrowthBookFeatures"`
		}
		if json.Unmarshal(data, &doc) == nil {
			// 1. Parse additionalModelOptionsCache
			for _, opt := range doc.AdditionalModelOptions {
				if opt.Value == "" || opt.Disabled {
					continue
				}
				slug := opt.Value
				if idx := strings.Index(slug, "["); idx != -1 {
					slug = slug[:idx]
				}
				label := opt.Label
				if label == "" {
					label = slug
				}
				modelMap[slug] = discoveredModel{
					id:          slug,
					displayName: label,
					description: opt.Description,
				}
			}

			// 2. Parse GrowthBook features for enabled velvet_mallet flags
			for k, v := range doc.GrowthBookFeatures {
				bVal, ok := v.(bool)
				if !ok || !bVal {
					continue
				}
				if strings.HasPrefix(k, "tengu_velvet_mallet_") {
					suffix := strings.TrimPrefix(k, "tengu_velvet_mallet_")
					slug := "claude-" + strings.ReplaceAll(suffix, "_", "-")

					// Format display name e.g. opus_5 -> "Opus 5", haiku_4_5 -> "Haiku 4.5", fable_5 -> "Fable 5.1"
					disp := formatModelName(suffix)
					if _, exists := modelMap[slug]; !exists {
						modelMap[slug] = discoveredModel{
							id:          slug,
							displayName: disp,
						}
					}
				}
			}
		}
	}

	if len(modelMap) == 0 {
		return nil, nil
	}

	// Stable sort order: Opus, Sonnet, Haiku, Fable, then others
	familyRank := func(id string) int {
		lower := strings.ToLower(id)
		switch {
		case strings.Contains(lower, "fable"):
			return 1
		case strings.Contains(lower, "opus"):
			return 2
		case strings.Contains(lower, "sonnet"):
			return 3
		case strings.Contains(lower, "haiku"):
			return 4
		default:
			return 5
		}
	}

	var models []provider.ModelInfo
	for _, m := range modelMap {
		isActive := (m.id == activeModel || (activeModel != "" && strings.Contains(activeModel, m.id)))
		models = append(models, provider.ModelInfo{
			Provider:        p.Name(),
			ID:              m.id,
			DisplayName:     m.displayName,
			Description:     m.description,
			ContextWindow:   200000,
			InputModalities: []string{"text", "image", "document"},
			ServiceTiers:    []string{"pro", "team", "enterprise"},
			Visibility:      "public",
			IsActive:        isActive,
		})
	}

	sort.Slice(models, func(i, j int) bool {
		rI := familyRank(models[i].ID)
		rJ := familyRank(models[j].ID)
		if rI != rJ {
			return rI < rJ
		}
		return models[i].ID < models[j].ID
	})

	return models, nil
}

func formatModelName(suffix string) string {
	parts := strings.Split(suffix, "_")
	var formatted []string
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if i+1 < len(parts) && isNumeric(p) && isNumeric(parts[i+1]) {
			// Join digits with dot, e.g. 4 and 5 -> 4.5
			formatted = append(formatted, p+"."+parts[i+1])
			i++
		} else {
			formatted = append(formatted, strings.Title(p))
		}
	}
	res := strings.Join(formatted, " ")
	if res == "Fable 5" {
		res = "Fable 5.1"
	}
	return res
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
