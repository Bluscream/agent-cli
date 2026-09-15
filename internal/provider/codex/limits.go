package codex

import (
	"agentcli.local/ai/internal/provider"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

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
