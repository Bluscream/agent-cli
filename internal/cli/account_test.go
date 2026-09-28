package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "agentcli.local/ai/internal/provider/codex"
)

// A provider without the ProfileManager capability is refused by name, rather
// than by comparing against a hardcoded pair of provider names.
func TestAccountMutationProviderValidation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DEBUG", "0")
	t.Setenv("AI_DEBUG", "0")

	for _, subcmd := range []string{"save", "switch", "fresh"} {
		args := []string{"account", subcmd, "-p", "codex"}
		if subcmd != "fresh" {
			args = append(args, "test-prof")
		}
		cmd := New(&bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{})
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "does not support account profiles") {
			t.Errorf("expected error about unsupported provider for %s, got %v", subcmd, err)
		}
	}
}

func TestAccountSwitchAmbiguity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEBUG", "0")
	t.Setenv("AI_DEBUG", "0")

	// Create profile with same name in both antigravity and claude
	agDir := filepath.Join(home, ".local/share/antigravity-profiles")
	_ = os.MkdirAll(agDir, 0700)
	_ = os.WriteFile(filepath.Join(agDir, "shared.json"), []byte(`{"db":{}}`), 0600)

	clDir := filepath.Join(home, ".local/share/claude-profiles/shared")
	_ = os.MkdirAll(clDir, 0700)
	_ = os.WriteFile(filepath.Join(clDir, "profile.json"), []byte(`{"name":"shared"}`), 0600)

	cmd := New(&bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{})
	cmd.SetArgs([]string{"account", "switch", "shared"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "specify -p <provider>") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
}

func TestAccountSaveClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEBUG", "0")
	t.Setenv("AI_DEBUG", "0")

	claudeDir := filepath.Join(home, ".config/Claude")
	_ = os.MkdirAll(claudeDir, 0700)
	_ = os.WriteFile(filepath.Join(claudeDir, "config.json"), []byte(`{"lastKnownAccountUuid":"uuid-1"}`), 0600)
	_ = os.WriteFile(filepath.Join(claudeDir, "Cookies"), []byte("cookies"), 0600)

	out := &bytes.Buffer{}
	cmd := New(&bytes.Buffer{}, out, &bytes.Buffer{})
	cmd.SetArgs([]string{"account", "save", "work", "-p", "claude"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("save claude failed: %v", err)
	}

	profMeta := filepath.Join(home, ".local/share/claude-profiles/work/profile.json")
	if _, err := os.Stat(profMeta); err != nil {
		t.Fatalf("profile.json missing: %v", err)
	}

	desktopFile := filepath.Join(home, "Desktop/claude-work.desktop")
	if _, err := os.Stat(desktopFile); err != nil {
		t.Fatalf("desktop file missing: %v", err)
	}
}
