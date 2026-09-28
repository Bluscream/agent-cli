package antigravity

import (
	"encoding/base64"
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

func (p *AntigravityProvider) ListConversations(opts provider.HistoryOptions) ([]provider.ConversationSummary, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".config/Antigravity IDE/User/globalStorage/state.vscdb")
	brainDir := filepath.Join(home, ".gemini/antigravity-ide/brain")
	convDir := filepath.Join(home, ".gemini/antigravity-ide/conversations")

	// Read state.vscdb trajectory summaries
	convoMap := make(map[string]*provider.ConversationSummary)

	// A state.vscdb that cannot be read is not fatal: the brain directory below
	// is an independent source of sessions, so listing degrades rather than
	// failing outright.
	if rawBytes, err := readVscdbB64Proto(dbPath, trajectorySummariesKey); err == nil {
		entries, _ := extractMapEntries(rawBytes)
		for cid, sub := range entries {
			convoMap[cid] = parseTrajectorySummary(cid, sub)
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
				c.TranscriptPath = tr
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
		c.Title = provider.CleanTitle(c.Title)
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
	matched, err := provider.Find(p, id)
	if err != nil {
		return nil, err
	}

	home, _ := os.UserHomeDir()
	brainPath := filepath.Join(home, ".gemini/antigravity-ide/brain", matched.ID)
	trPath := filepath.Join(brainPath, ".system_generated", "logs", "transcript.jsonl")

	turns := readTurns(trPath, 0) // read all turns
	initPrompt, lastResp := provider.Endpoints(turns)
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
	return provider.Audit(p, id)
}

// PendingTasks implements provider.AuditTasker. The IDE writes the
// conversation's own plan as an artifact, which is a better answer than the
// repository's task files.
func (p *AntigravityProvider) PendingTasks(detail *provider.ConversationDetail) []string {
	for _, art := range detail.Artifacts {
		if art.Name != "implementation_plan.md" {
			continue
		}
		content, err := os.ReadFile(art.Path)
		if err != nil {
			continue
		}
		return provider.PlanTasks(string(content))
	}
	return nil
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

// TranscriptRoots implements provider.TranscriptRooter.
func (p *AntigravityProvider) TranscriptRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return provider.ExistingDirs(
		filepath.Join(home, ".gemini/antigravity-ide/brain"),
		filepath.Join(home, ".gemini/antigravity/brain"),
	)
}
