package claude

import (
	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// GetActiveAccount returns the logged-in Claude account discovered from ~/.claude.json
// or ~/.config/Claude/config.json.
func (p *ClaudeProvider) GetActiveAccount() (*provider.AccountInfo, error) {
	home, _ := os.UserHomeDir()
	claudeJSONPath := filepath.Join(home, ".claude.json")

	if data, err := os.ReadFile(claudeJSONPath); err == nil {
		var doc struct {
			OAuthAccount struct {
				AccountUUID      string `json:"accountUuid"`
				EmailAddress     string `json:"emailAddress"`
				DisplayName      string `json:"displayName"`
				OrganizationType string `json:"organizationType"`
				BillingType      string `json:"billingType"`
			} `json:"oauthAccount"`
		}
		if json.Unmarshal(data, &doc) == nil && (doc.OAuthAccount.AccountUUID != "" || doc.OAuthAccount.EmailAddress != "") {
			disp := doc.OAuthAccount.DisplayName
			if disp == "" {
				disp = doc.OAuthAccount.EmailAddress
			}
			plan := doc.OAuthAccount.OrganizationType
			if plan == "claude_pro" {
				plan = "Claude Pro"
			} else if plan == "" {
				plan = doc.OAuthAccount.BillingType
			}
			return &provider.AccountInfo{
				Provider:    p.Name(),
				ID:          doc.OAuthAccount.AccountUUID,
				DisplayName: disp,
				Email:       doc.OAuthAccount.EmailAddress,
				Plan:        plan,
				IsActive:    true,
				ActiveIn:    "Claude Desktop",
				ConfigPath:  claudeJSONPath,
			}, nil
		}
	}

	// Fallback to config.json
	cfgPath := filepath.Join(home, ".config/Claude/config.json")
	if data, err := os.ReadFile(cfgPath); err == nil {
		var doc struct {
			LastKnownAccountUUID string `json:"lastKnownAccountUuid"`
		}
		if json.Unmarshal(data, &doc) == nil && doc.LastKnownAccountUUID != "" {
			return &provider.AccountInfo{
				Provider:    p.Name(),
				ID:          doc.LastKnownAccountUUID,
				DisplayName: idutil.ShortID(doc.LastKnownAccountUUID),
				Email:       "-",
				Plan:        "Claude Desktop",
				IsActive:    true,
				ActiveIn:    "Claude Desktop",
				ConfigPath:  cfgPath,
			}, nil
		}
	}

	return nil, fmt.Errorf("no active Claude account found")
}

// GetAccounts returns all accounts known to Claude.
func (p *ClaudeProvider) GetAccounts() ([]provider.AccountInfo, error) {
	active, err := p.GetActiveAccount()
	if err != nil || active == nil {
		return nil, nil
	}
	return []provider.AccountInfo{*active}, nil
}
