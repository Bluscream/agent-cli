package codex

import (
	"agentcli.local/ai/internal/fsutil"
	"agentcli.local/ai/internal/provider"
	"os"
	"path/filepath"
	"strings"
)

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

func (p *CodexProvider) ImportSkill(sourceDir string) error {
	home, _ := os.UserHomeDir()
	skillName := filepath.Base(sourceDir)
	destDir := filepath.Join(home, ".codex/skills", skillName)
	return fsutil.CopyDir(sourceDir, destDir)
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
