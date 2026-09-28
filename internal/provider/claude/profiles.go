package claude

import (
	"os"
	"path/filepath"

	"agentcli.local/ai/internal/provider"
)

// profilePath is where one profile's metadata lives. Named once here so the
// layout stays this package's business; the CLI used to reconstruct it.
func profilePath(name string) string {
	return filepath.Join(ProfilesDir(), name, "profile.json")
}

// ListProfiles implements provider.ProfileManager.
func (p *ClaudeProvider) ListProfiles() ([]provider.Profile, error) {
	infos, err := ListProfiles()
	if err != nil {
		return nil, err
	}
	profiles := make([]provider.Profile, 0, len(infos))
	for _, info := range infos {
		profiles = append(profiles, provider.Profile{
			Provider: p.Name(),
			Name:     info.Name,
			Path:     info.Path,
			// A saved Claude profile always carries the session it captured;
			// the account uuid is what identifies it.
			HasToken:   info.AccountUUID != "",
			Email:      info.Email,
			Plan:       info.Plan,
			Display:    info.DisplayName,
			AccountID:  info.AccountUUID,
			ModifiedAt: info.SavedAt,
		})
	}
	return profiles, nil
}

// ProfileExists implements provider.ProfileManager.
func (p *ClaudeProvider) ProfileExists(name string) bool {
	fi, err := os.Stat(profilePath(name))
	return err == nil && !fi.IsDir()
}

// SaveProfile implements provider.ProfileManager.
func (p *ClaudeProvider) SaveProfile(name string) error { return SaveProfile(name) }

// SwitchProfile implements provider.ProfileManager.
func (p *ClaudeProvider) SwitchProfile(name string) error { return SwitchProfile(name) }

// FreshSession implements provider.ProfileManager.
func (p *ClaudeProvider) FreshSession() error { return FreshSession() }

var _ provider.ProfileManager = (*ClaudeProvider)(nil)
