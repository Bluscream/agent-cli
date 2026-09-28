package provider

import (
	"strings"
	"testing"
)

// profiledProvider is a fakeProvider that manages profiles.
type profiledProvider struct {
	*fakeProvider
	profiles []Profile
	switched string
}

func (p *profiledProvider) ListProfiles() ([]Profile, error) { return p.profiles, nil }

func (p *profiledProvider) ProfileExists(name string) bool {
	for _, prof := range p.profiles {
		if prof.Name == name {
			return true
		}
	}
	return false
}

func (p *profiledProvider) SaveProfile(name string) error { return nil }

func (p *profiledProvider) SwitchProfile(name string) error {
	p.switched = name
	return nil
}

func (p *profiledProvider) FreshSession() error { return nil }

func profiled(name string, profileNames ...string) *profiledProvider {
	p := &profiledProvider{fakeProvider: &fakeProvider{name: name}}
	for _, n := range profileNames {
		p.profiles = append(p.profiles, Profile{Provider: name, Name: n})
	}
	return p
}

// "codex has no profiles" used to be a hardcoded name comparison, and a -p the
// check did not recognise fell through to Antigravity.
func TestResolveProfileManagerRejectsAProviderWithoutTheCapability(t *testing.T) {
	withRegistry(t, profiled("alpha", "work"), &fakeProvider{name: "plain"})

	_, _, err := ResolveProfileManager("plain", "work")
	if err == nil {
		t.Fatal("expected an error for a provider that cannot manage profiles")
	}
	if !strings.Contains(err.Error(), "does not support account profiles") {
		t.Fatalf("error does not say why: %v", err)
	}
}

// Resolution by profile name asks each provider whether it holds one, instead
// of the CLI reconstructing each provider's on-disk layout.
func TestResolveProfileManagerFindsTheProviderHoldingTheProfile(t *testing.T) {
	withRegistry(t, profiled("alpha", "work"), profiled("beta", "personal"))

	p, _, err := ResolveProfileManager("", "personal")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if p.Name() != "beta" {
		t.Fatalf("resolved to %q, want beta", p.Name())
	}
}

// Guessing here would restore the wrong account.
func TestResolveProfileManagerRejectsAProfileNameHeldByTwoProviders(t *testing.T) {
	withRegistry(t, profiled("alpha", "shared"), profiled("beta", "shared"))

	_, _, err := ResolveProfileManager("", "shared")
	if err == nil {
		t.Fatal("expected an ambiguous profile name to be an error")
	}
	for _, want := range []string{"alpha", "beta", "-p"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error does not mention %q: %v", want, err)
		}
	}
}

// A name no provider holds must be reported, not defaulted to one of them.
func TestResolveProfileManagerRejectsAnUnknownProfileName(t *testing.T) {
	withRegistry(t, profiled("alpha", "work"), profiled("beta", "personal"))

	if _, _, err := ResolveProfileManager("", "nonexistent"); err == nil {
		t.Fatal("expected an error for a profile no provider stores")
	}
}

func TestResolveProfileManagerNeedsAProviderWhenThereIsNothingToDisambiguateWith(t *testing.T) {
	withRegistry(t, profiled("alpha"), profiled("beta"))

	if _, _, err := ResolveProfileManager("", ""); err == nil {
		t.Fatal("expected `account fresh` with two capable providers to require -p")
	}
}

func TestProfileManagersNamesProvidersWithoutTheCapability(t *testing.T) {
	withRegistry(t, profiled("alpha"), &fakeProvider{name: "plain"})

	managers, unsupported, err := ProfileManagers("")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(managers) != 1 || managers[0].Name() != "alpha" {
		t.Fatalf("managers = %v", managers)
	}
	if len(unsupported) != 1 || unsupported[0] != "plain" {
		t.Fatalf("unsupported = %v, want [plain]", unsupported)
	}
}

func TestMergeAccountsMarksTheMatchingProfileActive(t *testing.T) {
	p := profiled("alpha")
	withRegistry(t, p)

	active := &AccountInfo{Provider: "alpha", ID: "uuid-1", Email: "a@example.com", IsActive: true}
	profiles := []Profile{
		{Name: "work", AccountID: "uuid-1", Email: "a@example.com"},
		{Name: "personal", AccountID: "uuid-2", Email: "b@example.com"},
	}
	byID := func(prof Profile, act AccountInfo) bool { return prof.AccountID == act.ID }

	accounts := MergeAccounts(p, active, profiles, byID)
	if len(accounts) != 2 {
		t.Fatalf("got %d accounts, want 2 (the active session matched a profile)", len(accounts))
	}
	if !accounts[0].IsActive || accounts[0].ID != "work" {
		t.Fatalf("wrong row marked active: %+v", accounts)
	}
	if accounts[0].ActiveIn != p.DisplayName() {
		t.Fatalf("ActiveIn = %q, want %q", accounts[0].ActiveIn, p.DisplayName())
	}
	if accounts[1].ActiveIn != "-" {
		t.Fatalf("inactive row labelled %q", accounts[1].ActiveIn)
	}
}

// A signed-in account with nothing saved for it must still be listed, or a
// signed-in agent would appear to have no account at all.
func TestMergeAccountsPrependsAnUnmatchedActiveSession(t *testing.T) {
	p := profiled("alpha")
	withRegistry(t, p)

	active := &AccountInfo{Provider: "alpha", ID: "uuid-9", Email: "nobody@example.com", IsActive: true}
	profiles := []Profile{{Name: "work", AccountID: "uuid-1"}}
	byID := func(prof Profile, act AccountInfo) bool { return prof.AccountID == act.ID }

	accounts := MergeAccounts(p, active, profiles, byID)
	if len(accounts) != 2 {
		t.Fatalf("got %d accounts, want 2", len(accounts))
	}
	if accounts[0].ID != "uuid-9" || !accounts[0].IsActive {
		t.Fatalf("the active session was not prepended: %+v", accounts)
	}
}

func TestMergeAccountsWithNoActiveSession(t *testing.T) {
	p := profiled("alpha")
	withRegistry(t, p)

	accounts := MergeAccounts(p, nil, []Profile{{Name: "work"}}, func(Profile, AccountInfo) bool { return true })
	if len(accounts) != 1 || accounts[0].IsActive {
		t.Fatalf("got %+v", accounts)
	}
}

// The display name prefers what the account calls itself over the profile name
// the user happened to choose.
func TestMergeAccountsPrefersTheAccountDisplayName(t *testing.T) {
	p := profiled("alpha")
	withRegistry(t, p)

	accounts := MergeAccounts(p, nil,
		[]Profile{{Name: "work", Display: "Real Name"}, {Name: "bare"}},
		func(Profile, AccountInfo) bool { return false })

	if accounts[0].DisplayName != "Real Name" {
		t.Fatalf("got %q, want the account's own name", accounts[0].DisplayName)
	}
	if accounts[1].DisplayName != "bare" {
		t.Fatalf("got %q, want the profile name as the fallback", accounts[1].DisplayName)
	}
}
