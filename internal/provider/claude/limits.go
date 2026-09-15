package claude

import (
	"agentcli.local/ai/internal/provider"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// GetLimits reads Claude's plan-usage-history.json and buddy-tokens.json to
// surface quota consumption without requiring an internet call.
//
//   - fh  = "fast" (Opus-class) message quota remaining as 0–100%. 0 = exhausted.
//   - sd  = "standard/daily" message quota remaining as 0–100%. 0 = exhausted.
//
// The window is 5 hours for fh and resets at midnight (UTC) for sd, but
// Claude does not store the exact reset time locally, so we approximate.
func (p *ClaudeProvider) GetLimits() ([]provider.LimitInfo, error) {
	home, _ := os.UserHomeDir()
	var limits []provider.LimitInfo
	now := time.Now()

	// --- plan-usage-history.json ---
	usagePath := filepath.Join(home, ".config/Claude/plan-usage-history.json")
	if data, err := os.ReadFile(usagePath); err == nil {
		var doc struct {
			Samples []struct {
				T   int64  `json:"t"` // Unix ms
				Org string `json:"org"`
				U   struct {
					FH int `json:"fh"` // fast/high-speed quota remaining %
					SD int `json:"sd"` // standard/daily remaining %
				} `json:"u"`
			} `json:"samples"`
		}
		if json.Unmarshal(data, &doc) == nil && len(doc.Samples) > 0 {
			last := doc.Samples[len(doc.Samples)-1]
			// fh: 5-hour rolling window; approximate next refill
			fhUsedPct := float64(100 - last.U.FH)
			fhRefill := time.Now().Add(5 * time.Hour).Truncate(5 * time.Hour)
			limits = append(limits, provider.LimitInfo{
				Provider:    p.Name(),
				Name:        "Fast messages (Opus-class)",
				Used:        -1,
				Limit:       -1,
				UsedPct:     fhUsedPct,
				Unit:        "%",
				RefillAt:    fhRefill,
				RefillEvery: "5 h rolling",
				Note:        fmt.Sprintf("%d%% remaining", last.U.FH),
			})
			// sd: daily reset at midnight UTC
			sdUsedPct := float64(100 - last.U.SD)
			nextMidnight := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day()+1, 0, 0, 0, 0, time.UTC)
			limits = append(limits, provider.LimitInfo{
				Provider:    p.Name(),
				Name:        "Standard messages (daily)",
				Used:        -1,
				Limit:       -1,
				UsedPct:     sdUsedPct,
				Unit:        "%",
				RefillAt:    nextMidnight,
				RefillEvery: "daily (midnight UTC)",
				Note:        fmt.Sprintf("%d%% remaining", last.U.SD),
			})
		}
	}

	// --- buddy-tokens.json ---
	tokPath := filepath.Join(home, ".config/Claude/buddy-tokens.json")
	if data, err := os.ReadFile(tokPath); err == nil {
		var doc struct {
			TokensToday struct {
				Date   string `json:"date"`
				Tokens int64  `json:"tokens"`
			} `json:"tokens-today"`
		}
		if json.Unmarshal(data, &doc) == nil && doc.TokensToday.Tokens > 0 {
			limits = append(limits, provider.LimitInfo{
				Provider:    p.Name(),
				Name:        "Tokens sent today",
				Used:        doc.TokensToday.Tokens,
				Limit:       -1,
				UsedPct:     -1,
				Unit:        "tokens",
				RefillAt:    time.Time{},
				RefillEvery: "daily",
				Note:        doc.TokensToday.Date,
			})
		}
	}

	return limits, nil
}
