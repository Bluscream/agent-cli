package codex

import (
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

	"agentcli.local/ai/internal/gitutil"
	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
)

type CodexProvider struct{}

func New() *CodexProvider {
	return &CodexProvider{}
}

func init() {
	provider.Register(New())
}

func (p *CodexProvider) Name() string {
	return "codex"
}

func (p *CodexProvider) DisplayName() string {
	return "Codex Desktop"
}

func (p *CodexProvider) Status() (provider.ProviderInfo, error) {
	home, _ := os.UserHomeDir()
	appImage := filepath.Join(home, "Applications/codex-desktop.AppImage")
	configPath := filepath.Join(home, ".codex/.codex-global-state.json")
	dataPath := filepath.Join(home, ".codex")

	installed := false
	if _, err := os.Stat(appImage); err == nil {
		installed = true
	}

	metrics := provider.FindProcesses("codex-desktop", "ChatGPT")

	info := provider.ProviderInfo{
		ID:             p.Name(),
		DisplayName:    p.DisplayName(),
		Origin:         "ilysenko/codex-desktop-linux (OpenAI deb repackager)",
		Installed:      installed,
		BinaryPath:     appImage,
		ConfigPath:     configPath,
		DataPath:       dataPath,
		Running:        metrics.Running,
		PIDs:           metrics.PIDs,
		CPUPercent:     metrics.CPUPercent,
		MemoryRSSBytes: metrics.MemoryRSS,
	}

	// Count MCP servers in plugins
	pluginsDir := filepath.Join(dataPath, "plugins")
	_ = filepath.Walk(pluginsDir, func(path string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && (strings.HasSuffix(path, ".mcp.json") || strings.HasSuffix(path, "desktop-mcp.json")) {
			info.MCPCount++
		}
		return nil
	})

	// Check busy: lockfiles in ~/.codex/tmp/ or recent writes to rollout files
	lockPattern := filepath.Join(dataPath, "tmp/arg0/*/.lock")
	if matches, err := filepath.Glob(lockPattern); err == nil && len(matches) > 0 {
		for _, m := range matches {
			if provider.IsFileRecentlyActive(m, 30*time.Second) {
				info.Busy = true
				info.ActiveTask = "Active agent execution lock"
				break
			}
		}
	}

	if !info.Busy {
		sessionsDir := filepath.Join(dataPath, "sessions")
		_ = filepath.Walk(sessionsDir, func(path string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() && strings.HasSuffix(path, ".jsonl") {
				if provider.IsFileRecentlyActive(path, 20*time.Second) {
					info.Busy = true
					info.ActiveTask = fmt.Sprintf("Writing rollout in %s", filepath.Base(path))
					return filepath.SkipAll
				}
			}
			return nil
		})
	}

	return info, nil
}

func (p *CodexProvider) ListConversations(opts provider.HistoryOptions) ([]provider.ConversationSummary, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".codex/state_5.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nil
	}

	// Query threads from state_5.sqlite
	// columns: id, title, created_at, updated_at, cwd, model, rollout_path
	query := "SELECT id, title, created_at, updated_at, cwd, model, rollout_path FROM threads ORDER BY updated_at DESC;"
	cmd := exec.Command("sqlite3", "-separator", "\x1f", dbPath, query)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to query codex state: %w", err)
	}

	var results []provider.ConversationSummary
	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "\x1f")
		if len(parts) < 7 {
			continue
		}

		id := parts[0]
		title := parts[1]
		createdSec, _ := strconv.ParseInt(parts[2], 10, 64)
		updatedSec, _ := strconv.ParseInt(parts[3], 10, 64)
		cwd := parts[4]
		model := parts[5]
		rolloutPath := parts[6]

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
			title = fmt.Sprintf("Codex %s", id[:8])
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

func (p *CodexProvider) ListMemories() ([]provider.MemoryItem, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".codex/memories_1.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nil
	}

	query := "SELECT thread_id, raw_memory, rollout_summary, rollout_slug, generated_at FROM stage1_outputs ORDER BY generated_at DESC;"
	cmd := exec.Command("sqlite3", "-separator", "\x1f", dbPath, query)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to read codex memories: %w", err)
	}

	var items []provider.MemoryItem
	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "\x1f")
		if len(parts) >= 5 {
			tid := parts[0]
			raw := parts[1]
			summary := parts[2]
			slug := parts[3]
			genSec, _ := strconv.ParseInt(parts[4], 10, 64)
			t := time.Unix(genSec, 0)

			title := slug
			if title == "" {
				title = summary
			}
			if title == "" {
				title = fmt.Sprintf("Memory for thread %s", tid[:8])
			}

			items = append(items, provider.MemoryItem{
				Provider:   p.Name(),
				ID:         tid,
				Title:      title,
				Content:    raw,
				CreatedAt:  t,
				UpdatedAt:  t,
				SourceFile: dbPath,
			})
		}
	}

	return items, nil
}

func (p *CodexProvider) BackupMemories(destDir string) (string, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".codex/memories_1.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return "", fmt.Errorf("codex memories db not found at %s", dbPath)
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}
	destFile := filepath.Join(destDir, fmt.Sprintf("codex-memories-%d.sqlite", time.Now().Unix()))

	cmd := exec.Command("sqlite3", dbPath, fmt.Sprintf(".backup '%s'", destFile))
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("sqlite backup failed: %w", err)
	}
	return destFile, nil
}

func (p *CodexProvider) PurgeMemories() (int, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".codex/memories_1.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return 0, nil
	}

	cmd := exec.Command("sqlite3", dbPath, "DELETE FROM stage1_outputs; SELECT changes();")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("failed to purge memories: %w", err)
	}

	cnt, _ := strconv.Atoi(strings.TrimSpace(out.String()))
	return cnt, nil
}

func (p *CodexProvider) ListSkills() ([]provider.SkillItem, error) {
	home, _ := os.UserHomeDir()
	skillsRoots := []struct {
		dir     string
		builtin bool
	}{
		{filepath.Join(home, ".codex/skills/.system"), true},
		{filepath.Join(home, ".codex/skills"), false},
	}

	var skills []provider.SkillItem
	seen := make(map[string]bool)

	for _, root := range skillsRoots {
		entries, err := os.ReadDir(root.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || e.Name() == ".system" {
				continue
			}
			if seen[e.Name()] {
				continue
			}
			seen[e.Name()] = true

			skillDir := filepath.Join(root.dir, e.Name())
			skillMD := filepath.Join(skillDir, "SKILL.md")
			desc := ""
			if data, err := os.ReadFile(skillMD); err == nil {
				desc = extractSkillDescription(string(data))
			}

			skills = append(skills, provider.SkillItem{
				Provider:    p.Name(),
				Name:        e.Name(),
				Description: desc,
				Path:        skillDir,
				IsBuiltin:   root.builtin,
			})
		}
	}

	return skills, nil
}

func (p *CodexProvider) ImportMemory(item provider.MemoryItem) error {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".codex/memories_1.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("codex memories sqlite db not found: %s", dbPath)
	}

	tid := item.ID
	if tid == "" {
		tid = fmt.Sprintf("imported-%d", time.Now().Unix())
	}
	title := item.Title
	if title == "" {
		title = item.ID
	}
	nowSec := time.Now().Unix()

	// Escape single quotes for SQLite
	safeRaw := strings.ReplaceAll(item.Content, "'", "''")
	safeSummary := strings.ReplaceAll(title, "'", "''")
	safeTid := strings.ReplaceAll(tid, "'", "''")

	query := fmt.Sprintf(
		"INSERT OR REPLACE INTO stage1_outputs (thread_id, source_updated_at, raw_memory, rollout_summary, rollout_slug, generated_at) VALUES ('%s', %d, '%s', '%s', '%s', %d);",
		safeTid, nowSec, safeRaw, safeSummary, safeSummary, nowSec,
	)
	cmd := exec.Command("sqlite3", dbPath, query)
	return cmd.Run()
}

func (p *CodexProvider) ImportSkill(sourceDir string) error {
	home, _ := os.UserHomeDir()
	skillName := filepath.Base(sourceDir)
	destDir := filepath.Join(home, ".codex/skills", skillName)
	return copyDir(sourceDir, destDir)
}

func (p *CodexProvider) PurgeSkills() (int, error) {
	home, _ := os.UserHomeDir()
	skillsDir := filepath.Join(home, ".codex/skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return 0, nil
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() && e.Name() != ".system" {
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

func (p *CodexProvider) ListPlugins() ([]provider.PluginItem, error) {
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".codex/plugins")
	var list []provider.PluginItem

	// 1. Check cached / installed plugins
	cacheDir := filepath.Join(pluginsDir, "cache")
	_ = filepath.Walk(cacheDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil || !fi.IsDir() {
			return nil
		}
		// If directory contains .codex-plugin, it's a plugin bundle
		manifest := filepath.Join(path, ".codex-plugin")
		if _, err := os.Stat(manifest); err == nil {
			rel, _ := filepath.Rel(cacheDir, path)
			parts := strings.Split(rel, string(os.PathSeparator))
			name := filepath.Base(path)
			if len(parts) >= 2 {
				name = parts[len(parts)-2] // plugin directory name before version
			}
			list = append(list, provider.PluginItem{
				Provider:    p.Name(),
				Name:        name,
				Type:        "Codex Plugin Bundle",
				Path:        path,
				Status:      "active",
				Description: fmt.Sprintf("Codex plugin extension (%s)", rel),
			})
		}
		return nil
	})

	// 2. Check plugin appserver host
	appserverDir := filepath.Join(pluginsDir, ".plugin-appserver")
	if entries, err := os.ReadDir(appserverDir); err == nil {
		for _, e := range entries {
			list = append(list, provider.PluginItem{
				Provider:    p.Name(),
				Name:        e.Name(),
				Type:        "App Server Host",
				Path:        filepath.Join(appserverDir, e.Name()),
				Status:      "installed",
				Description: fmt.Sprintf("Codex plugin host runtime (%s)", e.Name()),
			})
		}
	}

	return list, nil
}

func (p *CodexProvider) InstallPlugin(sourcePath string) error {
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".codex/plugins")
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
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destDir, filepath.Base(sourcePath)), data, 0644)
}

func (p *CodexProvider) UninstallPlugin(name string) error {
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(home, ".codex/plugins")
	// Look in plugins root, cache, or appserver
	targets := []string{
		filepath.Join(pluginsDir, name),
		filepath.Join(pluginsDir, "cache", "openai-bundled", name),
		filepath.Join(pluginsDir, "cache", "openai-curated-remote", name),
	}
	for _, t := range targets {
		if _, err := os.Stat(t); err == nil {
			return os.RemoveAll(t)
		}
	}
	return nil
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
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}

		if obj.Payload.Role == "" && len(obj.Payload.Content) == 0 {
			continue
		}

		role := obj.Payload.Role
		if role == "developer" {
			// Skip internal role instructions
			continue
		}

		var sb strings.Builder
		for _, c := range obj.Payload.Content {
			if c.Text != "" {
				sb.WriteString(c.Text)
			}
		}
		contentStr := strings.TrimSpace(sb.String())
		if contentStr == "" {
			continue
		}

		if len(contentStr) > 400 {
			contentStr = contentStr[:400] + "..."
		}

		ts := time.Now()
		if obj.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, obj.Timestamp); err == nil {
				ts = t
			}
		}

		all = append(all, provider.TurnInfo{
			StepIndex: step,
			Role:      role,
			Content:   contentStr,
			Timestamp: ts,
		})
	}

	if limit > 0 && len(all) > limit {
		turns = all[len(all)-limit:]
	} else {
		turns = all
	}

	return turns, step
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

// GetLimits for Codex. Codex does not store quota/usage data in any local
// file; usage is tracked server-side. We surface what we can from local state:
// the auth mode (chatgpt subscription vs API key) and last auth refresh time.
func (p *CodexProvider) GetLimits() ([]provider.LimitInfo, error) {
	home, _ := os.UserHomeDir()
	var limits []provider.LimitInfo

	// Read auth.json to determine subscription type
	authPath := filepath.Join(home, ".codex/auth.json")
	if data, err := os.ReadFile(authPath); err == nil {
		var doc struct {
			AuthMode    string `json:"auth_mode"`
			LastRefresh string `json:"last_refresh"`
		}
		if json.Unmarshal(data, &doc) == nil {
			authNote := doc.AuthMode
			if authNote == "" {
				authNote = "unknown"
			}
			refreshNote := ""
			if doc.LastRefresh != "" {
				if t, err := time.Parse(time.RFC3339Nano, doc.LastRefresh); err == nil {
					refreshNote = "Last refresh: " + t.Local().Format("2006-01-02 15:04")
				}
			}
			limits = append(limits, provider.LimitInfo{
				Provider:    p.Name(),
				Name:        "Subscription / Auth mode",
				Used:        -1,
				Limit:       -1,
				UsedPct:     -1,
				Unit:        "-",
				RefillEvery: "N/A",
				Note:        fmt.Sprintf("auth_mode=%s  %s", authNote, refreshNote),
			})
		}
	}

	// Codex tracks no token/message quota locally.
	limits = append(limits, provider.LimitInfo{
		Provider:    p.Name(),
		Name:        "Usage quota",
		Used:        -1,
		Limit:       -1,
		UsedPct:     -1,
		Unit:        "-",
		RefillEvery: "N/A",
		Note:        "Not tracked locally — check platform.openai.com/usage",
	})

	return limits, nil
}

// GetModels reads ~/.codex/models_cache.json which Codex keeps up-to-date
// with the full server model list, including context windows and modalities.
func (p *CodexProvider) GetModels() ([]provider.ModelInfo, error) {
	home, _ := os.UserHomeDir()
	cachePath := filepath.Join(home, ".codex/models_cache.json")

	// Read current model from config.toml so we can flag IsActive
	activeSlugs := map[string]bool{}
	cfgPath := filepath.Join(home, ".codex/config.toml")
	if raw, err := os.ReadFile(cfgPath); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "model =") {
				slug := strings.Trim(strings.TrimPrefix(line, "model ="), ` "`)
				activeSlugs[slug] = true
			}
		}
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, fmt.Errorf("models cache not found at %s: %w", cachePath, err)
	}

	var cacheDoc struct {
		FetchedAt string `json:"fetched_at"`
		Models    []struct {
			Slug                     string `json:"slug"`
			DisplayName              string `json:"display_name"`
			Description              string `json:"description"`
			DefaultReasoningLevel    string `json:"default_reasoning_level"`
			SupportedReasoningLevels []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
			Visibility      string   `json:"visibility"`
			ContextWindow   int64    `json:"context_window"`
			InputModalities []string `json:"input_modalities"`
			ServiceTiers    []struct {
				Name string `json:"name"`
			} `json:"service_tiers"`
		} `json:"models"`
	}

	if err := json.Unmarshal(data, &cacheDoc); err != nil {
		return nil, fmt.Errorf("failed to parse models cache: %w", err)
	}

	var models []provider.ModelInfo
	for _, m := range cacheDoc.Models {
		var reasoningLevels []string
		for _, r := range m.SupportedReasoningLevels {
			reasoningLevels = append(reasoningLevels, r.Effort)
		}
		var tiers []string
		for _, t := range m.ServiceTiers {
			tiers = append(tiers, t.Name)
		}
		models = append(models, provider.ModelInfo{
			Provider:        p.Name(),
			ID:              m.Slug,
			DisplayName:     m.DisplayName,
			Description:     m.Description,
			ContextWindow:   m.ContextWindow,
			InputModalities: m.InputModalities,
			ServiceTiers:    tiers,
			ReasoningLevels: reasoningLevels,
			IsActive:        activeSlugs[m.Slug],
			Visibility:      m.Visibility,
		})
	}

	return models, nil
}

// GetActiveAccount returns the active logged in account for Codex from auth.json and .codex-global-state.json.
func (p *CodexProvider) GetActiveAccount() (*provider.AccountInfo, error) {
	home, _ := os.UserHomeDir()
	authPath := filepath.Join(home, ".codex/auth.json")

	authMode := "unknown"
	if data, err := os.ReadFile(authPath); err == nil {
		var doc struct {
			AuthMode string `json:"auth_mode"`
		}
		if json.Unmarshal(data, &doc) == nil && doc.AuthMode != "" {
			authMode = doc.AuthMode
		}
	}

	accID := ""
	statePath := filepath.Join(home, ".codex/.codex-global-state.json")
	if data, err := os.ReadFile(statePath); err == nil {
		var doc struct {
			ElectronPersistedAtomState struct {
				MCPExtensionSidebarCatalog struct {
					AccountID string `json:"accountId"`
				} `json:"mcp-extension-sidebar-catalog"`
			} `json:"electron-persisted-atom-state"`
		}
		if json.Unmarshal(data, &doc) == nil && doc.ElectronPersistedAtomState.MCPExtensionSidebarCatalog.AccountID != "" {
			accID = doc.ElectronPersistedAtomState.MCPExtensionSidebarCatalog.AccountID
		}
	}

	displayID := accID
	if len(displayID) > 8 {
		displayID = displayID[:8]
	}
	if displayID == "" {
		displayID = "active"
	}

	plan := authMode
	if authMode == "chatgpt" {
		plan = "ChatGPT (Subscription)"
	}

	return &provider.AccountInfo{
		Provider:    p.Name(),
		ID:          accID,
		DisplayName: fmt.Sprintf("ChatGPT (%s)", displayID),
		Email:       "-",
		Plan:        plan,
		IsActive:    true,
		ActiveIn:    "Codex Desktop",
		ConfigPath:  authPath,
	}, nil
}

// GetAccounts returns all accounts known to Codex.
func (p *CodexProvider) GetAccounts() ([]provider.AccountInfo, error) {
	active, err := p.GetActiveAccount()
	if err != nil || active == nil {
		return nil, nil
	}
	return []provider.AccountInfo{*active}, nil
}
