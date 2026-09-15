package codex

import (
	"agentcli.local/ai/internal/provider"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// GetActiveAccount returns the active logged in account for Codex from auth.json and .codex-global-state.json.
func (p *CodexProvider) GetActiveAccount() (*provider.AccountInfo, error) {
	home, _ := os.UserHomeDir()
	authPath := filepath.Join(home, ".codex/auth.json")
	if _, err := os.Stat(authPath); err != nil {
		return nil, err
	}

	authMode := "unknown"
	if data, err := os.ReadFile(authPath); err == nil {
		var doc struct {
			AuthMode string `json:"auth_mode"`
		}
		if json.Unmarshal(data, &doc) == nil && doc.AuthMode != "" {
			authMode = doc.AuthMode
		}
	}

	accID := ""
	statePath := filepath.Join(home, ".codex/.codex-global-state.json")
	if data, err := os.ReadFile(statePath); err == nil {
		var doc struct {
			ElectronPersistedAtomState struct {
				MCPExtensionSidebarCatalog struct {
					AccountID string `json:"accountId"`
				} `json:"mcp-extension-sidebar-catalog"`
			} `json:"electron-persisted-atom-state"`
		}
		if json.Unmarshal(data, &doc) == nil && doc.ElectronPersistedAtomState.MCPExtensionSidebarCatalog.AccountID != "" {
			accID = doc.ElectronPersistedAtomState.MCPExtensionSidebarCatalog.AccountID
		}
	}

	displayID := accID
	if len(displayID) > 8 {
		displayID = displayID[:8]
	}
	if displayID == "" {
		displayID = "active"
	}

	plan := authMode
	if authMode == "chatgpt" {
		plan = "ChatGPT (Subscription)"
	}

	return &provider.AccountInfo{
		Provider:    p.Name(),
		ID:          accID,
		DisplayName: fmt.Sprintf("ChatGPT (%s)", displayID),
		Email:       "-",
		Plan:        plan,
		IsActive:    true,
		ActiveIn:    "Codex Desktop",
		ConfigPath:  authPath,
	}, nil
}

// GetAccounts returns all accounts known to Codex.
func (p *CodexProvider) GetAccounts() ([]provider.AccountInfo, error) {
	active, err := p.GetActiveAccount()
	if err != nil || active == nil {
		return nil, nil
	}
	return []provider.AccountInfo{*active}, nil
}
