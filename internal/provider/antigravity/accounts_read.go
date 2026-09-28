package antigravity

import (
	"agentcli.local/ai/internal/provider"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

// parseUserStatus extracts display name, email, and subscription plan name from
// antigravityUnifiedStateSync.userStatus protobuf data.
func parseUserStatus(rawB64 string) (displayName, email, plan string) {
	if rawB64 == "" {
		return "", "", ""
	}
	raw, err := base64.StdEncoding.DecodeString(rawB64)
	if err != nil {
		return "", "", ""
	}
	sm := extractSentinelMap(raw)
	v, ok := sm["userStatusSentinelKey"]
	if !ok {
		// Try parsing directly if already decoded
		v = raw
	}
	for _, f := range parseMsg(v) {
		switch {
		case f.fieldNum == 3 && f.wireType == 2:
			displayName = string(f.data)
		case f.fieldNum == 7 && f.wireType == 2:
			email = string(f.data)
		case f.fieldNum == 36 && f.wireType == 2:
			for _, pf := range parseMsg(f.data) {
				if pf.fieldNum == 2 && pf.wireType == 2 {
					plan = string(pf.data)
				}
			}
		}
	}
	return displayName, email, plan
}

// GetActiveAccount returns the active logged in account from Antigravity IDE state.vscdb.
func (p *AntigravityProvider) GetActiveAccount() (*provider.AccountInfo, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".config/Antigravity IDE/User/globalStorage/state.vscdb")
	raw, err := readVscdbB64Proto(dbPath, "antigravityUnifiedStateSync.userStatus")
	if err != nil {
		return nil, err
	}
	disp, email, plan := parseUserStatus(base64.StdEncoding.EncodeToString(raw))
	if disp == "" && email == "" {
		return nil, fmt.Errorf("no active Antigravity account found")
	}
	return &provider.AccountInfo{
		Provider:    p.Name(),
		ID:          "active",
		DisplayName: disp,
		Email:       email,
		Plan:        plan,
		IsActive:    true,
		ActiveIn:    "Antigravity IDE",
		ConfigPath:  dbPath,
	}, nil
}

// GetAccounts returns the active session and every saved profile.
func (p *AntigravityProvider) GetAccounts() ([]provider.AccountInfo, error) {
	active, _ := p.GetActiveAccount()
	profiles, err := p.ListProfiles()
	if err != nil {
		return nil, err
	}
	// An Antigravity profile records no account id, so the email is the only
	// identity available.
	return provider.MergeAccounts(p, active, profiles, func(prof provider.Profile, active provider.AccountInfo) bool {
		return prof.Email != "" && prof.Email == active.Email
	}), nil
}
