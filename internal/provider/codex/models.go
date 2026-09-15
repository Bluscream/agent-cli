package codex

import (
	"agentcli.local/ai/internal/provider"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
