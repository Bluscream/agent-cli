package claude

import (
	"agentcli.local/ai/internal/provider"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

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
			runes := []rune(p)
			if len(runes) > 0 {
				runes[0] = unicode.ToUpper(runes[0])
			}
			formatted = append(formatted, string(runes))
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
