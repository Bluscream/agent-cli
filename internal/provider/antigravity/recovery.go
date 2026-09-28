package antigravity

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentcli.local/ai/internal/provider"
	"agentcli.local/ai/internal/sqlite"
)

type RecoveryReport struct {
	PristineEntriesCount int      `json:"pristine_entries_count"`
	DiskConversations    int      `json:"disk_conversations"`
	GhostCount           int      `json:"ghost_count"`
	RecoveredCount       int      `json:"recovered_count"`
	TotalFinalCount      int      `json:"total_final_count"`
	RecoveredIDs         []string `json:"recovered_ids"`
	BackupPath           string   `json:"backup_path,omitempty"`
	DryRun               bool     `json:"dry_run"`
}

// Recover scans for lost or unindexed conversations and updates state.vscdb
func Recover(dryRun bool, cleanGhosts bool) (*RecoveryReport, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".config/Antigravity IDE/User/globalStorage/state.vscdb")
	convDir := filepath.Join(home, ".gemini/antigravity-ide/conversations")
	brainDir := filepath.Join(home, ".gemini/antigravity-ide/brain")

	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("could not find globalStorage database at: %s", dbPath)
	}

	report := &RecoveryReport{DryRun: dryRun}

	// 1. Fetch current trajectorySummaries from state.vscdb. A missing key is
	// an empty index, which is recoverable; a database that would not open is
	// not, so only the first is tolerated.
	rawBytes, readErr := readVscdbB64Proto(dbPath, trajectorySummariesKey)

	originalEntriesMap := make(map[string][]byte)
	if readErr == nil {
		parsed, err := extractMapEntries(rawBytes)
		if err != nil {
			return nil, fmt.Errorf("invalid trajectory index: %w", err)
		}
		for k, v := range parsed {
			originalEntriesMap[k] = v
		}

	}
	report.PristineEntriesCount = len(originalEntriesMap)

	// 2. Discover all conversation IDs from disk
	diskConvos := make(map[string]bool)
	if entries, err := os.ReadDir(convDir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".db") {
				stem := strings.TrimSuffix(e.Name(), ".db")
				if len(stem) == 36 && strings.Count(stem, "-") == 4 {
					diskConvos[stem] = true
				}
			}
		}
	}

	if entries, err := os.ReadDir(brainDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				stem := e.Name()
				if len(stem) == 36 && strings.Count(stem, "-") == 4 {
					diskConvos[stem] = true
				}
			}
		}
	}
	report.DiskConversations = len(diskConvos)

	// 3. Find ghost empty conversations
	ghosts := make(map[string]bool)
	allCandidateIDs := make(map[string]bool)
	for cid := range diskConvos {
		allCandidateIDs[cid] = true
	}
	for cid := range originalEntriesMap {
		allCandidateIDs[cid] = true
	}

	for cid := range allCandidateIDs {
		if isGhostConversation(cid, convDir, brainDir) {
			ghosts[cid] = true
		}
	}
	report.GhostCount = len(ghosts)

	// 4. Assemble final entries
	finalBytes := new(bytes.Buffer)
	addedCIDs := make(map[string]bool)

	for cid, subBytes := range originalEntriesMap {
		if cleanGhosts && ghosts[cid] {
			continue
		}
		finalBytes.Write(encodeFieldBytes(1, subBytes))
		addedCIDs[cid] = true
	}

	// 5. Ingest missing unindexed conversations
	var missing []string
	for cid := range diskConvos {
		if !addedCIDs[cid] {
			if cleanGhosts && ghosts[cid] {
				continue
			}
			missing = append(missing, cid)
		}
	}
	sort.Strings(missing)

	for _, cid := range missing {
		info := extractConvoInfo(cid, convDir, brainDir)
		entryBytes, err := buildSummaryEntry(info)
		if err == nil {
			finalBytes.Write(entryBytes)
			addedCIDs[cid] = true
			report.RecoveredIDs = append(report.RecoveredIDs, cid)
		}
	}
	report.RecoveredCount = len(report.RecoveredIDs)
	report.TotalFinalCount = len(addedCIDs)

	if dryRun {
		return report, nil
	}

	// 6. Write backup and commit to sqlite
	backupPath := fmt.Sprintf("%s.bak.%d", dbPath, time.Now().Unix())
	if err := sqlite.Backup(dbPath, backupPath); err != nil {
		return nil, fmt.Errorf("failed to create db backup: %w", err)
	}

	report.BackupPath = backupPath

	updatedB64 := base64.StdEncoding.EncodeToString(finalBytes.Bytes())
	if err := sqlite.Exec(dbPath, "UPDATE ItemTable SET value = ? WHERE key = ?;", updatedB64, trajectorySummariesKey); err != nil {
		return nil, fmt.Errorf("failed to update state.vscdb: %w", err)
	}

	// Clean ghosts from disk if requested
	if cleanGhosts {
		for g := range ghosts {
			gBrain := filepath.Join(brainDir, g)
			gDB := filepath.Join(convDir, g+".db")
			if fi, err := os.Stat(gDB); err == nil && fi.Size() == 0 {
				_ = os.Remove(gDB)
			}
			if entries, err := os.ReadDir(gBrain); err == nil && len(entries) == 0 {
				_ = os.Remove(gBrain)
			}
		}
	}

	return report, nil
}

func isGhostConversation(cid, convDir, brainDir string) bool {
	dbPath := filepath.Join(convDir, cid+".db")
	brainPath := filepath.Join(brainDir, cid)
	trPath := filepath.Join(brainPath, ".system_generated", "logs", "transcript.jsonl")

	if fi, err := os.Stat(dbPath); err == nil && fi.Size() > 0 {
		return false
	}
	if fi, err := os.Stat(trPath); err == nil && fi.Size() > 0 {
		return false
	}
	entries, err := os.ReadDir(brainPath)
	if err == nil && len(entries) > 0 {
		return false
	}
	return true
}

func extractConvoInfo(cid, convDir, brainDir string) ConvoMeta {
	meta := ConvoMeta{
		ID:           cid,
		TrajectoryID: cid,
		WorkspaceURI: "file:///run/media/system/Data/Projects",
		StepCount:    1,
		CreatedTS:    time.Now().Unix(),
		ModifiedTS:   time.Now().Unix(),
	}

	trPath := filepath.Join(brainDir, cid, ".system_generated", "logs", "transcript.jsonl")
	if fi, err := os.Stat(trPath); err == nil {
		meta.CreatedTS = fi.ModTime().Unix()
		meta.ModifiedTS = fi.ModTime().Unix()
		if f, err := os.Open(trPath); err == nil {
			defer f.Close()
			sc := bufio.NewScanner(f)
			lineNum := 0
			for sc.Scan() && lineNum < 30 {
				lineNum++
				line := sc.Text()
				if meta.Title == "" && strings.Contains(line, "<USER_REQUEST>") {
					parts := strings.Split(line, "<USER_REQUEST>")
					if len(parts) > 1 {
						req := strings.Split(parts[1], "</USER_REQUEST>")[0]
						meta.Title = provider.CleanTitle(req)
					}
				}
				if meta.Title != "" {
					break
				}
			}
		}
	} else if dirFi, err := os.Stat(filepath.Join(brainDir, cid)); err == nil {
		meta.CreatedTS = dirFi.ModTime().Unix()
		meta.ModifiedTS = dirFi.ModTime().Unix()
	}

	if meta.Title == "" {
		meta.Title = fmt.Sprintf("Conversation %s", cid[:8])
	}

	uriBytes := []byte(meta.WorkspaceURI)
	cidBytes := []byte(cid)
	meta.MetaBlob = append(
		encodeFieldBytes(1, encodeFieldBytes(1, uriBytes)),
		append(
			encodeFieldBytes(3, cidBytes),
			append(encodeFieldBytes(6, cidBytes), encodeFieldBytes(7, uriBytes)...)...,
		)...,
	)

	return meta
}
