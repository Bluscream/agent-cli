package antigravity

import (
	"agentcli.local/ai/internal/fsutil"
	"agentcli.local/ai/internal/provider"
	"os"
	"path/filepath"
	"strings"
)

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

func (p *AntigravityProvider) ImportSkill(sourceDir string) error {
	home, _ := os.UserHomeDir()
	skillName := filepath.Base(sourceDir)
	destDir := filepath.Join(home, ".gemini/config/skills", skillName)
	return fsutil.CopyDir(sourceDir, destDir)
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
