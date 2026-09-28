package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeProvider is a minimal Provider for testing the shared purge plumbing.
// Only the methods the plumbing calls do anything.
type fakeProvider struct {
	name    string
	convos  []ConversationSummary
	purgeAt func(id string, opts PurgeOptions) (*Purge, error)
}

func (f *fakeProvider) Name() string        { return f.name }
func (f *fakeProvider) DisplayName() string { return f.name }
func (f *fakeProvider) Status() (ProviderInfo, error) {
	return ProviderInfo{ID: f.name}, nil
}

func (f *fakeProvider) ListConversations(opts HistoryOptions) ([]ConversationSummary, error) {
	return f.convos, nil
}
func (f *fakeProvider) GetConversation(string) (*ConversationDetail, error) { return nil, nil }
func (f *fakeProvider) AuditConversation(string) (*AuditDossier, error)     { return nil, nil }
func (f *fakeProvider) ListMemories() ([]MemoryItem, error)                 { return nil, nil }
func (f *fakeProvider) BackupMemories(string) (string, error)               { return "", nil }
func (f *fakeProvider) PurgeMemories() (int, error)                         { return 0, nil }
func (f *fakeProvider) ImportMemory(MemoryItem) error                       { return nil }
func (f *fakeProvider) ListSkills() ([]SkillItem, error)                    { return nil, nil }
func (f *fakeProvider) ImportSkill(string) error                            { return nil }
func (f *fakeProvider) PurgeSkills() (int, error)                           { return 0, nil }
func (f *fakeProvider) ListPlugins() ([]PluginItem, error)                  { return nil, nil }
func (f *fakeProvider) InstallPlugin(string) error                          { return nil }
func (f *fakeProvider) UninstallPlugin(string) error                        { return nil }
func (f *fakeProvider) GetLimits() ([]LimitInfo, error)                     { return nil, nil }
func (f *fakeProvider) GetModels() ([]ModelInfo, error)                     { return nil, nil }
func (f *fakeProvider) GetAccounts() ([]AccountInfo, error)                 { return nil, nil }
func (f *fakeProvider) GetActiveAccount() (*AccountInfo, error)             { return nil, nil }
func (f *fakeProvider) RecoverConversations(bool) (string, error)           { return "", nil }

// purgingProvider adds the optional capability.
type purgingProvider struct {
	*fakeProvider
	purged []string
}

func (p *purgingProvider) PurgeConversation(id string, opts PurgeOptions) (*Purge, error) {
	if p.purgeAt != nil {
		return p.purgeAt(id, opts)
	}
	p.purged = append(p.purged, id)
	return &Purge{Provider: p.name, ID: id, DryRun: opts.DryRun, FreedBytes: 10}, nil
}

func (p *purgingProvider) PurgeAllConversations(opts PurgeOptions) ([]Purge, error) {
	var out []Purge
	for _, c := range p.convos {
		p.purged = append(p.purged, c.ID)
		out = append(out, Purge{Provider: p.name, ID: c.ID, DryRun: opts.DryRun, FreedBytes: 10})
	}
	return out, nil
}

// withRegistry replaces the registry for one test and restores it afterwards,
// so tests never depend on which real providers this host has installed.
func withRegistry(t *testing.T, entries ...Provider) {
	t.Helper()
	registryMu.Lock()
	oldProviders, oldOrder := providers, order
	providers, order = make(map[string]Provider), nil
	registryMu.Unlock()

	t.Cleanup(func() {
		registryMu.Lock()
		providers, order = oldProviders, oldOrder
		registryMu.Unlock()
	})
	for _, e := range entries {
		Register(e)
	}
}

func TestSelectErrorsWhenNothingIsRegistered(t *testing.T) {
	withRegistry(t)
	if _, err := Select(""); err == nil {
		t.Fatal("expected an error when no providers are registered")
	}
}

// The defect this replaces: four resolution sites, and only one noticed that a
// prefix could match in two providers.
func TestLocateRejectsAnIdMatchingSeveralProviders(t *testing.T) {
	shared := "11111111-2222-3333-4444-555555555555"
	withRegistry(t,
		&purgingProvider{fakeProvider: &fakeProvider{name: "alpha", convos: []ConversationSummary{{ID: shared}}}},
		&purgingProvider{fakeProvider: &fakeProvider{name: "beta", convos: []ConversationSummary{{ID: shared}}}},
	)

	_, err := Locate("", shared)
	if err == nil {
		t.Fatal("expected an ambiguous id to be an error, not a silent first match")
	}
	for _, want := range []string{"alpha", "beta"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error does not name %q: %v", want, err)
		}
	}
}

func TestLocateNarrowedByProviderIsNotAmbiguous(t *testing.T) {
	shared := "11111111-2222-3333-4444-555555555555"
	withRegistry(t,
		&purgingProvider{fakeProvider: &fakeProvider{name: "alpha", convos: []ConversationSummary{{ID: shared}}}},
		&purgingProvider{fakeProvider: &fakeProvider{name: "beta", convos: []ConversationSummary{{ID: shared}}}},
	)
	located, err := Locate("alpha", shared)
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if located.Provider.Name() != "alpha" {
		t.Fatalf("got provider %q", located.Provider.Name())
	}
}

// "Delete everything" must not quietly mean "everything from the providers that
// happened to support it".
func TestRunPurgeNamesProvidersThatCannotDelete(t *testing.T) {
	withRegistry(t,
		&purgingProvider{fakeProvider: &fakeProvider{name: "can", convos: []ConversationSummary{{ID: "a"}}}},
		&fakeProvider{name: "cannot", convos: []ConversationSummary{{ID: "b"}}},
	)

	summary, err := RunPurge(PurgeTarget{}, PurgeOptions{DryRun: true})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if summary.Conversations != 1 {
		t.Fatalf("purged %d conversations, want 1", summary.Conversations)
	}
	if len(summary.Unsupported) != 1 || summary.Unsupported[0] != "cannot" {
		t.Fatalf("unsupported providers: %v, want [cannot]", summary.Unsupported)
	}
}

func TestRunPurgeAllCoversEveryProvider(t *testing.T) {
	alpha := &purgingProvider{fakeProvider: &fakeProvider{name: "alpha", convos: []ConversationSummary{{ID: "a1"}, {ID: "a2"}}}}
	beta := &purgingProvider{fakeProvider: &fakeProvider{name: "beta", convos: []ConversationSummary{{ID: "b1"}}}}
	withRegistry(t, alpha, beta)

	summary, err := RunPurge(PurgeTarget{}, PurgeOptions{})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if summary.Conversations != 3 || summary.Removed != 3 {
		t.Fatalf("got %d conversations / %d removed, want 3/3", summary.Conversations, summary.Removed)
	}
	if summary.FreedBytes != 30 {
		t.Fatalf("freed %d bytes, want 30", summary.FreedBytes)
	}
}

func TestRunPurgeOneProviderOnly(t *testing.T) {
	alpha := &purgingProvider{fakeProvider: &fakeProvider{name: "alpha", convos: []ConversationSummary{{ID: "a1"}}}}
	beta := &purgingProvider{fakeProvider: &fakeProvider{name: "beta", convos: []ConversationSummary{{ID: "b1"}}}}
	withRegistry(t, alpha, beta)

	if _, err := RunPurge(PurgeTarget{Provider: "alpha"}, PurgeOptions{}); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if len(beta.purged) != 0 {
		t.Fatalf("purging alpha also purged beta: %v", beta.purged)
	}
}

func TestPurgeWithWarningsIsNotCounted(t *testing.T) {
	summary := &PurgeSummary{}
	bad := Purge{Provider: "alpha", ID: "a"}
	bad.Warnf("could not remove %s", "something")
	summary.Add(bad)

	if summary.Removed != 0 || summary.Failed != 1 {
		t.Fatalf("a purge with warnings counted as removed: %+v", summary)
	}
}

// RemovePath's confinement: a path outside the root is refused, not followed.
func TestRemovePathRefusesAPathOutsideTheRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	outside := filepath.Join(base, "outside.txt")
	if err := os.WriteFile(outside, []byte("keep me"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	purge := Purge{}
	purge.RemovePath(root, outside, PurgeOptions{})

	if len(purge.Warnings) == 0 {
		t.Fatal("expected a warning for a path outside the root")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("the file outside the root was deleted: %v", err)
	}
}

func TestRemovePathDeletesInsideTheRootAndCountsBytes(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "conversation.jsonl")
	if err := os.WriteFile(target, []byte("0123456789"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	purge := Purge{}
	purge.RemovePath(root, target, PurgeOptions{})

	if !purge.Complete() {
		t.Fatalf("unexpected warnings: %v", purge.Warnings)
	}
	if purge.FreedBytes != 10 {
		t.Fatalf("freed %d bytes, want 10", purge.FreedBytes)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("the file survived: %v", err)
	}
}

// A dry run must report exactly what a real run would remove, and remove
// nothing.
func TestRemovePathDryRunReportsWithoutDeleting(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "conversation.jsonl")
	if err := os.WriteFile(target, []byte("0123456789"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	purge := Purge{}
	purge.RemovePath(root, target, PurgeOptions{DryRun: true})

	if len(purge.RemovedPaths) != 1 || purge.RemovedPaths[0] != target {
		t.Fatalf("dry run reported %v", purge.RemovedPaths)
	}
	if purge.FreedBytes != 10 {
		t.Fatalf("dry run measured %d bytes, want 10", purge.FreedBytes)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("dry run deleted the file: %v", err)
	}
}

func TestRemovePathIgnoresAMissingPath(t *testing.T) {
	root := t.TempDir()
	purge := Purge{}
	purge.RemovePath(root, filepath.Join(root, "gone.jsonl"), PurgeOptions{})

	if len(purge.RemovedPaths) != 0 {
		t.Fatalf("reported removing a path that was not there: %v", purge.RemovedPaths)
	}
	if !purge.Complete() {
		t.Fatalf("unexpected warnings: %v", purge.Warnings)
	}
}

// Describe feeds the confirmation prompt, so it must name exactly the scope
// that will be deleted.
func TestPurgeTargetDescribe(t *testing.T) {
	cases := []struct {
		target PurgeTarget
		want   string
	}{
		{PurgeTarget{}, "every conversation of every provider"},
		{PurgeTarget{Provider: "codex"}, "every conversation of codex"},
		{PurgeTarget{ID: "abc"}, "conversation abc"},
		{PurgeTarget{ID: "abc", Provider: "codex"}, "conversation abc of codex"},
	}
	for _, c := range cases {
		if got := c.target.Describe(); got != c.want {
			t.Errorf("Describe() = %q, want %q", got, c.want)
		}
	}
}
