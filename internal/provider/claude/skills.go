package claude

import (
	"agentcli.local/ai/internal/fsutil"
	"agentcli.local/ai/internal/provider"
	"os"
	"path/filepath"
	"strings"
)

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

func (p *ClaudeProvider) ImportSkill(sourceDir string) error {
	home, _ := os.UserHomeDir()
	skillName := filepath.Base(sourceDir)
	destDir := filepath.Join(home, ".claude/skills", skillName)
	return fsutil.CopyDir(sourceDir, destDir)
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
