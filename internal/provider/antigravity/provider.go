package antigravity

import (
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

	"agentcli.local/ai/internal/gitutil"
	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
)

type AntigravityProvider struct{}

func New() *AntigravityProvider {
	return &AntigravityProvider{}
}

func init() {
	provider.Register(New())
}

func (p *AntigravityProvider) Name() string {
	return "antigravity"
}

func (p *AntigravityProvider) DisplayName() string {
	return "Antigravity IDE"
}

func (p *AntigravityProvider) Status() (provider.ProviderInfo, error) {
	home, _ := os.UserHomeDir()
	binPath := "/var/home/linuxbrew/.linuxbrew/bin/antigravity-ide"
	configPath := filepath.Join(home, ".config/Antigravity IDE/User/globalStorage/state.vscdb")
	dataPath := filepath.Join(home, ".gemini/antigravity-ide")

	installed := false
	if _, err := os.Stat(binPath); err == nil {
		installed = true
	} else if _, err := exec.LookPath("antigravity-ide"); err == nil {
		installed = true
		binPath, _ = exec.LookPath("antigravity-ide")
	}

	metrics := provider.FindProcesses("antigravity-ide")

	info := provider.ProviderInfo{
		ID:             p.Name(),
		DisplayName:    p.DisplayName(),
		Origin:         "ublue-os/tap/antigravity-ide-linux (Homebrew)",
		Installed:      installed,
		BinaryPath:     binPath,
		ConfigPath:     configPath,
		DataPath:       dataPath,
		Running:        metrics.Running,
		PIDs:           metrics.PIDs,
		CPUPercent:     metrics.CPUPercent,
		MemoryRSSBytes: metrics.MemoryRSS,
	}

	// Count MCP servers configured in antigravity
	mcpPath := filepath.Join(dataPath, "mcp_config.json")
	if data, err := os.ReadFile(mcpPath); err == nil {
		var mcpDoc struct {
			MCPServers map[string]any `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &mcpDoc); err == nil {
			info.MCPCount = len(mcpDoc.MCPServers)
		}
	}

	// Determine if busy (activity in any brain transcript or running task log within 15s)
	brainDir := filepath.Join(dataPath, "brain")
	if entries, err := os.ReadDir(brainDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				tr := filepath.Join(brainDir, e.Name(), ".system_generated", "logs", "transcript.jsonl")
				if provider.IsFileRecentlyActive(tr, 20*time.Second) {
					info.Busy = true
					info.ActiveTask = fmt.Sprintf("Active turn in conversation %s", e.Name()[:8])
					break
				}
			}
		}
	}

	return info, nil
}

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

			// If this was an unindexed brain dir with 0 artifacts and no transcript, skip ghost
			if !exists && artCount == 0 {
				tr := filepath.Join(brainDir, cid, ".system_generated", "logs", "transcript.jsonl")
				if _, err := os.Stat(tr); err != nil {
					delete(convoMap, cid)
				}
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

func (p *AntigravityProvider) ListMemories() ([]provider.MemoryItem, error) {
	home, _ := os.UserHomeDir()
	knowledgeDirs := []string{
		filepath.Join(home, ".gemini/antigravity-ide/knowledge"),
		filepath.Join(home, ".gemini/config/knowledge"),
		filepath.Join(home, ".gemini/knowledge"),
	}

	var items []provider.MemoryItem
	for _, kd := range knowledgeDirs {
		entries, err := os.ReadDir(kd)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			metaPath := filepath.Join(kd, e.Name(), "metadata.json")
			data, err := os.ReadFile(metaPath)
			if err != nil {
				continue
			}
			var meta struct {
				Summary   string `json:"summary"`
				Title     string `json:"title"`
				CreatedAt string `json:"created_at"`
				UpdatedAt string `json:"updated_at"`
			}
			_ = json.Unmarshal(data, &meta)

			fi, _ := e.Info()
			modTime := time.Now()
			if fi != nil {
				modTime = fi.ModTime()
			}

			title := meta.Title
			if title == "" {
				title = e.Name()
			}

			items = append(items, provider.MemoryItem{
				Provider:   p.Name(),
				ID:         e.Name(),
				Title:      title,
				Content:    meta.Summary,
				SourceFile: metaPath,
				CreatedAt:  modTime,
				UpdatedAt:  modTime,
			})
		}
	}
	return items, nil
}

func (p *AntigravityProvider) BackupMemories(destDir string) (string, error) {
	items, err := p.ListMemories()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}
	destFile := filepath.Join(destDir, fmt.Sprintf("antigravity-memories-%d.json", time.Now().Unix()))
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(destFile, data, 0644); err != nil {
		return "", err
	}
	return destFile, nil
}

func (p *AntigravityProvider) PurgeMemories() (int, error) {
	items, err := p.ListMemories()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, item := range items {
		if item.SourceFile != "" {
			parent := filepath.Dir(item.SourceFile)
			if err := os.RemoveAll(parent); err == nil {
				count++
			}
		}
	}
	return count, nil
}

func (p *AntigravityProvider) ListSkills() ([]provider.SkillItem, error) {
	home, _ := os.UserHomeDir()
	searchRoots := []struct {
		dir     string
		builtin bool
	}{
		{filepath.Join(home, ".gemini/antigravity-ide/builtin/skills"), true},
		{filepath.Join(home, ".gemini/config/skills"), false},
		{".agents/skills", false},
	}

	var skills []provider.SkillItem
	for _, root := range searchRoots {
		entries, err := os.ReadDir(root.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			skillDir := filepath.Join(root.dir, e.Name())
			skillMD := filepath.Join(skillDir, "SKILL.md")
			desc := ""
			if content, err := os.ReadFile(skillMD); err == nil {
				desc = extractSkillDescription(string(content))
			}

			rulesCount := 0
			if rEntries, err := os.ReadDir(filepath.Join(skillDir, "rules")); err == nil {
				rulesCount = len(rEntries)
			}
			scriptsCount := 0
			if sEntries, err := os.ReadDir(filepath.Join(skillDir, "scripts")); err == nil {
				scriptsCount = len(sEntries)
			}

			skills = append(skills, provider.SkillItem{
				Provider:     p.Name(),
				Name:         e.Name(),
				Description:  desc,
				Path:         skillDir,
				IsBuiltin:    root.builtin,
				RulesCount:   rulesCount,
				ScriptsCount: scriptsCount,
			})
		}
	}
	return skills, nil
}

func (p *AntigravityProvider) ImportMemory(item provider.MemoryItem) error {
	home, _ := os.UserHomeDir()
	slug := idutil.ShortID(item.ID)
	if slug == "" {
		slug = fmt.Sprintf("imported-%d", time.Now().Unix())
	}
	targetDir := filepath.Join(home, ".gemini/antigravity-ide/knowledge", slug)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}

	meta := map[string]any{
		"title":      item.Title,
		"summary":    item.Content,
		"created_at": item.CreatedAt.Format(time.RFC3339),
		"updated_at": item.UpdatedAt.Format(time.RFC3339),
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	_ = os.WriteFile(filepath.Join(targetDir, "metadata.json"), metaBytes, 0644)

	// Also write content as a markdown document inside artifacts/
	artDir := filepath.Join(targetDir, "artifacts")
	_ = os.MkdirAll(artDir, 0755)
	_ = os.WriteFile(filepath.Join(artDir, "memory.md"), []byte(item.Content), 0644)
	return nil
}

func (p *AntigravityProvider) ImportSkill(sourceDir string) error {
	home, _ := os.UserHomeDir()
	skillName := filepath.Base(sourceDir)
	destDir := filepath.Join(home, ".gemini/config/skills", skillName)
	return copyDir(sourceDir, destDir)
}

func (p *AntigravityProvider) PurgeSkills() (int, error) {
	home, _ := os.UserHomeDir()
	configSkills := filepath.Join(home, ".gemini/config/skills")
	entries, err := os.ReadDir(configSkills)
	if err != nil {
		return 0, nil
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			if err := os.RemoveAll(filepath.Join(configSkills, e.Name())); err == nil {
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

func (p *AntigravityProvider) ListPlugins() ([]provider.PluginItem, error) {
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".gemini/config/plugins")
	var list []provider.PluginItem

	entries, err := os.ReadDir(pluginsDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			pDir := filepath.Join(pluginsDir, e.Name())
			pJSON := filepath.Join(pDir, "plugin.json")
			desc := "Antigravity plugin bundle (skills, rules, MCPs)"
			if data, err := os.ReadFile(pJSON); err == nil {
				var doc struct {
					Description string `json:"description"`
				}
				if err := json.Unmarshal(data, &doc); err == nil && doc.Description != "" {
					desc = doc.Description
				}
			}
			list = append(list, provider.PluginItem{
				Provider:    p.Name(),
				Name:        e.Name(),
				Type:        "Customization Bundle",
				Path:        pDir,
				Status:      "installed",
				Description: desc,
			})
		}
	}
	return list, nil
}

func (p *AntigravityProvider) InstallPlugin(sourcePath string) error {
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".gemini/config/plugins")
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		return err
	}

	name := filepath.Base(sourcePath)
	destDir := filepath.Join(pluginsDir, name)
	fi, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return copyDir(sourcePath, destDir)
	}
	// If a single file, create a plugin directory and put it there
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destDir, filepath.Base(sourcePath)), data, 0644)
}

func (p *AntigravityProvider) UninstallPlugin(name string) error {
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".gemini/config/plugins")
	return os.RemoveAll(filepath.Join(pluginsDir, name))
}

func (p *AntigravityProvider) RecoverConversations(dryRun bool) (string, error) {
	report, err := Recover(dryRun, true)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	return string(data), nil
}

// Helpers

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

		if len(contentStr) > 400 {
			contentStr = contentStr[:400] + "..."
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
		Title:     fmt.Sprintf("Conversation %s", cid[:8]),
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

// readVscdbB64Proto queries a key from the Antigravity IDE state.vscdb via
// sqlite3 and returns the outer protobuf bytes (already base64-decoded).
func readVscdbB64Proto(dbPath, key string) ([]byte, error) {
	var out bytes.Buffer
	cmd := exec.Command("sqlite3", dbPath, fmt.Sprintf("SELECT value FROM ItemTable WHERE key='%s';", key))
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	b64 := strings.TrimSpace(out.String())
	if b64 == "" {
		return nil, fmt.Errorf("key %q not found", key)
	}
	return base64.StdEncoding.DecodeString(b64)
}

// extractSentinelMap extracts a map[key]→inner-proto-bytes from the
// antigravityUnifiedStateSync.* style protobuf format:
//
//	outer: field1 = repeated { field1=string_key, field2=bytes{ field1=b64_value } }
//
// The b64_value is itself decoded and returned as raw bytes.
func extractSentinelMap(outerBytes []byte) map[string][]byte {
	result := make(map[string][]byte)
	for _, f := range parseMsg(outerBytes) {
		if f.fieldNum != 1 || f.wireType != 2 {
			continue
		}
		var key string
		var valBytes []byte
		for _, sf := range parseMsg(f.data) {
			switch {
			case sf.fieldNum == 1 && sf.wireType == 2:
				key = string(sf.data)
			case sf.fieldNum == 2 && sf.wireType == 2:
				// inner value: field1 = b64-encoded proto
				for _, vf := range parseMsg(sf.data) {
					if vf.fieldNum == 1 && vf.wireType == 2 {
						decoded, err := base64.StdEncoding.DecodeString(string(vf.data))
						if err == nil {
							valBytes = decoded
						}
					}
				}
			}
		}
		if key != "" {
			result[key] = valBytes
		}
	}
	return result
}

// GetLimits reads the Antigravity IDE state database to surface AI credit
// balance, plan name, and minimum credit threshold — all stored locally in
// antigravityUnifiedStateSync.userStatus and .modelCredits.
func (p *AntigravityProvider) GetLimits() ([]provider.LimitInfo, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".config/Antigravity IDE/User/globalStorage/state.vscdb")
	var limits []provider.LimitInfo

	// ── modelCredits ──────────────────────────────────────────────────────
	// Reads: useAICredits (bool), availableCredits (int), minimumCreditAmount (int)
	useCredits := false
	availableCredits := int64(-1)
	minimumCredits := int64(-1)

	if raw, err := readVscdbB64Proto(dbPath, "antigravityUnifiedStateSync.modelCredits"); err == nil {
		sm := extractSentinelMap(raw)
		if v, ok := sm["useAICreditsSentinelKey"]; ok {
			for _, f := range parseMsg(v) {
				if f.fieldNum == 1 && f.wireType == 0 {
					useCredits = f.varint == 1
				}
			}
		}
		if v, ok := sm["availableCreditsSentinelKey"]; ok {
			for _, f := range parseMsg(v) {
				if f.fieldNum == 2 && f.wireType == 0 {
					availableCredits = int64(f.varint)
				}
			}
		}
		if v, ok := sm["minimumCreditAmountForUsageKey"]; ok {
			for _, f := range parseMsg(v) {
				if f.fieldNum == 2 && f.wireType == 0 {
					minimumCredits = int64(f.varint)
				}
			}
		}
	}

	// ── userStatus ────────────────────────────────────────────────────────
	// field36 = subscription/plan info sub-message
	//   sub_field1  = plan ID  (e.g. "g1-pro-tier")
	//   sub_field2  = plan name (e.g. "Google AI Pro")
	//   sub_field14 = credits sub-proto { field1=?, field2=available, field3=minimum }
	planID := ""
	planName := ""
	creditsFromStatus := int64(-1)

	if raw, err := readVscdbB64Proto(dbPath, "antigravityUnifiedStateSync.userStatus"); err == nil {
		sm := extractSentinelMap(raw)
		if v, ok := sm["userStatusSentinelKey"]; ok {
			for _, topF := range parseMsg(v) {
				if topF.fieldNum == 36 && topF.wireType == 2 {
					// plan info sub-message
					for _, pf := range parseMsg(topF.data) {
						switch {
						case pf.fieldNum == 1 && pf.wireType == 2:
							planID = string(pf.data)
						case pf.fieldNum == 2 && pf.wireType == 2:
							planName = string(pf.data)
						case pf.fieldNum == 14 && pf.wireType == 2:
							// credits sub-proto: field2=available, field3=minimum
							for _, cf := range parseMsg(pf.data) {
								if cf.fieldNum == 2 && cf.wireType == 0 {
									creditsFromStatus = int64(cf.varint)
								}
								if cf.fieldNum == 3 && cf.wireType == 0 && minimumCredits < 0 {
									minimumCredits = int64(cf.varint)
								}
							}
						}
					}
					break // only process first field36
				}
			}
		}
	}

	// Prefer the credit value from userStatus (more reliable)
	if creditsFromStatus >= 0 {
		availableCredits = creditsFromStatus
	}

	// ── Emit limits ───────────────────────────────────────────────────────
	planLabel := planID
	if planName != "" {
		planLabel = planName
		if planID != "" {
			planLabel = planName + " (" + planID + ")"
		}
	}
	if planLabel == "" {
		planLabel = "unknown"
	}

	limits = append(limits, provider.LimitInfo{
		Provider:    p.Name(),
		Name:        "Subscription plan",
		Used:        -1,
		Limit:       -1,
		UsedPct:     -1,
		Unit:        "-",
		RefillEvery: "N/A",
		Note:        planLabel,
	})

	if useCredits || availableCredits >= 0 {
		creditNote := ""
		usedPct := float64(-1)
		if availableCredits >= 0 && minimumCredits > 0 {
			creditNote = fmt.Sprintf("min required: %d", minimumCredits)
		}
		limits = append(limits, provider.LimitInfo{
			Provider:    p.Name(),
			Name:        "Available AI Credits",
			Used:        -1,
			Limit:       availableCredits,
			UsedPct:     usedPct,
			Unit:        "credits",
			RefillEvery: "N/A",
			Note:        creditNote,
		})
	} else {
		limits = append(limits, provider.LimitInfo{
			Provider:    p.Name(),
			Name:        "AI Credits",
			Used:        -1,
			Limit:       -1,
			UsedPct:     -1,
			Unit:        "-",
			RefillEvery: "N/A",
			Note:        "credits not in use or not available locally",
		})
	}

	limits = append(limits, provider.LimitInfo{
		Provider:    p.Name(),
		Name:        "Token / request quota",
		Used:        -1,
		Limit:       -1,
		UsedPct:     -1,
		Unit:        "-",
		RefillEvery: "N/A",
		Note:        "Not tracked locally — check aistudio.google.com/app/apikey or console.cloud.google.com",
	})

	return limits, nil
}

// GetModels reads available models from antigravityUnifiedStateSync.userStatus.
// field33 contains repeated sub_field1 entries, each a model sub-message
// with field1 = model display name.
func (p *AntigravityProvider) GetModels() ([]provider.ModelInfo, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".config/Antigravity IDE/User/globalStorage/state.vscdb")

	// Try to read active model from modelPreferences
	activeModel := ""
	if raw, err := readVscdbB64Proto(dbPath, "antigravityUnifiedStateSync.modelPreferences"); err == nil {
		sm := extractSentinelMap(raw)
		if v, ok := sm["last_selected_agent_model_sentinel_key"]; ok {
			for _, f := range parseMsg(v) {
				if f.wireType == 2 {
					activeModel = string(f.data)
				} else if f.wireType == 0 {
					switch f.varint {
					case 1318:
						activeModel = "Gemini 3.6 Flash (High)"
					case 1319:
						activeModel = "Gemini 3.6 Flash (Medium)"
					case 1320:
						activeModel = "Gemini 3.6 Flash (Low)"
					case 1315:
						activeModel = "Gemini 3.5 Flash (High)"
					case 1316:
						activeModel = "Gemini 3.5 Flash (Medium)"
					case 1317:
						activeModel = "Gemini 3.5 Flash (Low)"
					case 1311:
						activeModel = "Gemini 3.1 Pro (High)"
					case 1312:
						activeModel = "Gemini 3.1 Pro (Low)"
					case 1301:
						activeModel = "Claude Sonnet 4.6 (Thinking)"
					case 1302:
						activeModel = "Claude Opus 4.6 (Thinking)"
					}
				}
			}
		}
	}

	// Read models from userStatus field33
	var models []provider.ModelInfo
	if raw, err := readVscdbB64Proto(dbPath, "antigravityUnifiedStateSync.userStatus"); err == nil {
		sm := extractSentinelMap(raw)
		if v, ok := sm["userStatusSentinelKey"]; ok {
			for _, topF := range parseMsg(v) {
				if topF.fieldNum != 33 || topF.wireType != 2 {
					continue
				}
				for _, mf := range parseMsg(topF.data) {
					if mf.fieldNum != 1 || mf.wireType != 2 {
						continue
					}
					var name string
					var mimeTypes []string
					for _, ff := range parseMsg(mf.data) {
						if ff.fieldNum == 1 && ff.wireType == 2 {
							name = string(ff.data)
						}
						if ff.fieldNum == 18 && ff.wireType == 2 {
							// repeated InputModality { field1=mime_type }
							for _, mim := range parseMsg(ff.data) {
								if mim.fieldNum == 1 && mim.wireType == 2 {
									mimeTypes = append(mimeTypes, string(mim.data))
								}
							}
						}
					}
					if name != "" {
						models = append(models, provider.ModelInfo{
							Provider:        p.Name(),
							ID:              "", // Antigravity only stores display names locally, no separate model ID/slug
							DisplayName:     name,
							ContextWindow:   1048576,
							InputModalities: normalizeMimeTypes(mimeTypes),
							IsActive:        name == activeModel,
							Visibility:      "internal",
						})
					}
				}
			}
		}
	}

	return models, nil
}

// parseUserStatus extracts display name, email, and subscription plan name from
// antigravityUnifiedStateSync.userStatus protobuf data.
func parseUserStatus(rawB64 string) (displayName, email, plan string) {
	if rawB64 == "" {
		return "", "", ""
	}
	raw, err := base64.StdEncoding.DecodeString(rawB64)
	if err != nil {
		return "", "", ""
	}
	sm := extractSentinelMap(raw)
	v, ok := sm["userStatusSentinelKey"]
	if !ok {
		// Try parsing directly if already decoded
		v = raw
	}
	for _, f := range parseMsg(v) {
		switch {
		case f.fieldNum == 3 && f.wireType == 2:
			displayName = string(f.data)
		case f.fieldNum == 7 && f.wireType == 2:
			email = string(f.data)
		case f.fieldNum == 36 && f.wireType == 2:
			for _, pf := range parseMsg(f.data) {
				if pf.fieldNum == 2 && pf.wireType == 2 {
					plan = string(pf.data)
				}
			}
		}
	}
	return displayName, email, plan
}

// GetActiveAccount returns the active logged in account from Antigravity IDE state.vscdb.
func (p *AntigravityProvider) GetActiveAccount() (*provider.AccountInfo, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".config/Antigravity IDE/User/globalStorage/state.vscdb")
	raw, err := readVscdbB64Proto(dbPath, "antigravityUnifiedStateSync.userStatus")
	if err != nil {
		return nil, err
	}
	disp, email, plan := parseUserStatus(base64.StdEncoding.EncodeToString(raw))
	if disp == "" && email == "" {
		return nil, fmt.Errorf("no active Antigravity account found")
	}
	return &provider.AccountInfo{
		Provider:    p.Name(),
		ID:          "active",
		DisplayName: disp,
		Email:       email,
		Plan:        plan,
		IsActive:    true,
		ActiveIn:    "Antigravity IDE",
		ConfigPath:  dbPath,
	}, nil
}

// GetAccounts returns all accounts known to Antigravity, including active session and saved profiles.
func (p *AntigravityProvider) GetAccounts() ([]provider.AccountInfo, error) {
	var accounts []provider.AccountInfo
	active, _ := p.GetActiveAccount()
	activeEmail := ""
	if active != nil {
		activeEmail = active.Email
	}

	profiles, _ := ListProfiles()
	seenProfiles := make(map[string]bool)

	for _, prof := range profiles {
		disp, email, plan := parseUserStatus(prof.UserStatus)
		if disp == "" {
			disp = prof.Name
		}
		isActive := (email != "" && activeEmail != "" && email == activeEmail)
		activeIn := "-"
		if isActive {
			activeIn = "Antigravity IDE"
		}
		accounts = append(accounts, provider.AccountInfo{
			Provider:    p.Name(),
			ID:          prof.Name,
			DisplayName: disp,
			Email:       email,
			Plan:        plan,
			IsActive:    isActive,
			ActiveIn:    activeIn,
			ConfigPath:  prof.Path,
		})
		seenProfiles[prof.Name] = true
	}

	// If the active account exists and didn't match any saved profile, prepend it
	if active != nil {
		foundActive := false
		for _, acc := range accounts {
			if acc.IsActive {
				foundActive = true
				break
			}
		}
		if !foundActive {
			accounts = append([]provider.AccountInfo{*active}, accounts...)
		}
	}

	return accounts, nil
}

// normalizeMimeTypes collapses a list of MIME types (e.g. "image/png", "image/webp",
// "video/mp4") into a deduplicated list of top-level modality names
// ("image", "video", "audio", "text", "document").
func normalizeMimeTypes(mimes []string) []string {
	seen := make(map[string]bool)
	order := []string{"text", "image", "audio", "video", "document"}
	for _, m := range mimes {
		slash := strings.Index(m, "/")
		if slash <= 0 {
			continue
		}
		top := m[:slash]
		switch top {
		case "application":
			seen["document"] = true
		default:
			seen[top] = true
		}
	}
	var result []string
	for _, o := range order {
		if seen[o] {
			result = append(result, o)
		}
	}
	return result
}
