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

// GetAccounts returns all accounts known to Claude, including the active session and saved profiles.
func (p *ClaudeProvider) GetAccounts() ([]provider.AccountInfo, error) {
	var accounts []provider.AccountInfo
	active, _ := p.GetActiveAccount()
	activeEmail := ""
	activeID := ""
	if active != nil {
		activeEmail = active.Email
		activeID = active.ID
	}

	profiles, _ := ListProfiles()

	for _, prof := range profiles {
		isActive := false
		if active != nil {
			if prof.AccountUUID != "" && activeID != "" && prof.AccountUUID == activeID {
				isActive = true
			} else if prof.Email != "" && activeEmail != "" && prof.Email == activeEmail {
				isActive = true
			}
		}
		activeIn := "-"
		if isActive {
			activeIn = "Claude Desktop"
		}
		disp := prof.DisplayName
		if disp == "" {
			disp = prof.Name
		}
		accounts = append(accounts, provider.AccountInfo{
			Provider:    p.Name(),
			ID:          prof.Name,
			DisplayName: disp,
			Email:       prof.Email,
			Plan:        prof.Plan,
			IsActive:    isActive,
			ActiveIn:    activeIn,
			ConfigPath:  prof.Path,
		})
	}

	// If the active account exists and didn't match any saved profile, prepend it
	if active != nil {
		foundActive := false
		for _, acc := range accounts {
			if acc.IsActive {
				foundActive = true
				break
			}
		}
		if !foundActive {
			accounts = append([]provider.AccountInfo{*active}, accounts...)
		}
	}

	return accounts, nil
}
