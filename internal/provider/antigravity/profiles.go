package antigravity

import (
	"os"
	"path/filepath"

	"agentcli.local/ai/internal/provider"
)

// profilePath is where one profile is stored. Antigravity keeps a single JSON
// file per profile where Claude keeps a directory; that difference is exactly
// what the CLI should not have known.
func profilePath(name string) string {
	return filepath.Join(ProfilesDir(), name+".json")
}

// ListProfiles implements provider.ProfileManager.
func (p *AntigravityProvider) ListProfiles() ([]provider.Profile, error) {
	infos, err := ListProfiles()
	if err != nil {
		return nil, err
	}
	profiles := make([]provider.Profile, 0, len(infos))
	for _, info := range infos {
		// The saved userStatus blob is the only place a profile records who it
		// belongs to.
		display, email, plan := parseUserStatus(info.UserStatus)
		profiles = append(profiles, provider.Profile{
			Provider:   p.Name(),
			Name:       info.Name,
			Path:       info.Path,
			HasToken:   info.HasToken,
			Display:    display,
			Email:      email,
			Plan:       plan,
			ModifiedAt: info.ModifiedAt,
		})
	}
	return profiles, nil
}

// ProfileExists implements provider.ProfileManager.
func (p *AntigravityProvider) ProfileExists(name string) bool {
	fi, err := os.Stat(profilePath(name))
	return err == nil && !fi.IsDir()
}

// SaveProfile implements provider.ProfileManager.
func (p *AntigravityProvider) SaveProfile(name string) error { return SaveProfile(name) }

// SwitchProfile implements provider.ProfileManager.
func (p *AntigravityProvider) SwitchProfile(name string) error { return SwitchProfile(name) }

// FreshSession implements provider.ProfileManager.
func (p *AntigravityProvider) FreshSession() error { return FreshSession() }

var _ provider.ProfileManager = (*AntigravityProvider)(nil)
