package provider

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Profile is one saved account profile.
type Profile struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	// Path is where the profile is stored, which is the provider's business;
	// it is reported rather than reconstructed by callers.
	Path     string `json:"path"`
	HasToken bool   `json:"has_token"`
	Email    string `json:"email,omitempty"`
	Plan     string `json:"plan,omitempty"`
	// Display is the account's own name when the profile recorded one, which
	// reads better than the profile name the user happened to choose.
	Display string `json:"display_name,omitempty"`
	// AccountID is the provider's identifier for the account, when the profile
	// stored one. Claude compares it to decide whether a profile is the active
	// session; Antigravity has only the email.
	AccountID  string    `json:"account_id,omitempty"`
	ModifiedAt time.Time `json:"modified_at"`
}

// ProfileManager is an optional capability: a provider whose signed-in session
// can be saved, restored and cleared.
//
// It exists because the CLI knew each provider's on-disk profile layout —
// <dir>/<name>.json for one, <dir>/<name>/profile.json for the other — and
// decided which provider to act on by comparing name strings. A layout change
// broke profile-name resolution silently, and "codex has no profiles" was a
// hardcoded comparison rather than a missing implementation.
type ProfileManager interface {
	// ListProfiles returns the saved profiles, newest state as stored.
	ListProfiles() ([]Profile, error)
	// ProfileExists reports whether a profile of this name is stored. It
	// belongs to the provider because only the provider knows the layout.
	ProfileExists(name string) bool
	// SaveProfile captures the current session under a name.
	SaveProfile(name string) error
	// SwitchProfile restores a saved profile and restarts the application.
	SwitchProfile(name string) error
	// FreshSession clears the current session and starts unauthenticated.
	FreshSession() error
}

// ProfileManagers returns the selected providers that can manage profiles,
// along with the names of those that cannot, so a caller can say so rather
// than silently acting on a subset.
func ProfileManagers(providerName string) (managers []Provider, unsupported []string, err error) {
	providers, err := Select(providerName)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range providers {
		if _, ok := p.(ProfileManager); ok {
			managers = append(managers, p)
			continue
		}
		unsupported = append(unsupported, p.Name())
	}
	sort.Strings(unsupported)
	return managers, unsupported, nil
}

// ResolveProfileManager picks the provider to act on for a profile operation.
//
// With a provider named, that one — and an error if it cannot manage profiles,
// rather than falling through to whichever provider is first. With no provider
// named and a profile name given, the provider that actually stores a profile
// of that name; two providers storing it is an error naming them, since
// guessing would restore the wrong account.
func ResolveProfileManager(providerName, profileName string) (Provider, ProfileManager, error) {
	if providerName != "" {
		p, err := Get(providerName)
		if err != nil {
			return nil, nil, err
		}
		manager, ok := p.(ProfileManager)
		if !ok {
			return nil, nil, fmt.Errorf("%s does not support account profiles", p.DisplayName())
		}
		return p, manager, nil
	}

	managers, _, err := ProfileManagers("")
	if err != nil {
		return nil, nil, err
	}
	if len(managers) == 0 {
		return nil, nil, fmt.Errorf("no provider supports account profiles")
	}

	if profileName == "" {
		// Nothing to disambiguate with, so the caller has to choose.
		if len(managers) > 1 {
			return nil, nil, fmt.Errorf("specify -p <provider>: %s all support account profiles", joinNames(managers))
		}
		return managers[0], managers[0].(ProfileManager), nil
	}

	var holders []Provider
	for _, p := range managers {
		if p.(ProfileManager).ProfileExists(profileName) {
			holders = append(holders, p)
		}
	}
	switch len(holders) {
	case 1:
		return holders[0], holders[0].(ProfileManager), nil
	case 0:
		return nil, nil, fmt.Errorf("no profile named %q is stored by %s", profileName, joinNames(managers))
	}
	return nil, nil, fmt.Errorf("profile %q exists for %s; specify -p <provider>", profileName, joinNames(holders))
}

func joinNames(providers []Provider) string {
	names := make([]string, 0, len(providers))
	for _, p := range providers {
		names = append(names, p.Name())
	}
	sort.Strings(names)
	return strings.Join(names, " and ")
}

// MergeAccounts builds the account list for a provider: one row per saved
// profile, with the active session prepended when it matches none of them.
//
// This was the same 45 lines in the Claude and Antigravity packages, differing
// only in which fields establish identity — Claude compares account uuid or
// email, Antigravity email alone. That difference is the identity argument, so
// the merge itself is shared.
//
// A profile whose identity matches the active session is marked active and
// labelled with the provider's display name, which is where the hardcoded
// "Claude Desktop" and "Antigravity IDE" strings in the two copies came from.
func MergeAccounts(p Provider, active *AccountInfo, profiles []Profile, sameAccount func(Profile, AccountInfo) bool) []AccountInfo {
	accounts := make([]AccountInfo, 0, len(profiles)+1)
	matchedActive := false

	for _, prof := range profiles {
		isActive := active != nil && sameAccount(prof, *active)
		if isActive {
			matchedActive = true
		}
		activeIn := "-"
		if isActive {
			activeIn = p.DisplayName()
		}
		display := prof.Display
		if display == "" {
			display = prof.Name
		}
		accounts = append(accounts, AccountInfo{
			Provider:    p.Name(),
			ID:          prof.Name,
			DisplayName: display,
			Email:       prof.Email,
			Plan:        prof.Plan,
			IsActive:    isActive,
			ActiveIn:    activeIn,
			ConfigPath:  prof.Path,
		})
	}

	// The signed-in account is worth reporting even with nothing saved for it:
	// omitting it would present a signed-in agent as having no account.
	if active != nil && !matchedActive {
		accounts = append([]AccountInfo{*active}, accounts...)
	}
	return accounts
}
