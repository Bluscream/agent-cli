package antigravity

import (
	"agentcli.local/ai/internal/provider"
	"os"
	"path/filepath"
	"strings"
)

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
