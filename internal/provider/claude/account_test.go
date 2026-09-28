package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestClaudeEnvironment(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	origStop := stopClaudeFunc
	origLaunch := launchClaudeFunc
	origSqlite := execSqliteFunc
	stopClaudeFunc = func() bool { return true }
	launchClaudeFunc = func() error { return nil }
	execSqliteFunc = func(cookieDBPath, query string, stdin string) (string, error) {
		return "", nil
	}
	t.Cleanup(func() {
		stopClaudeFunc = origStop
		launchClaudeFunc = origLaunch
		execSqliteFunc = origSqlite
	})

	claudeDir := filepath.Join(home, ".config/Claude")
	if err := os.MkdirAll(filepath.Join(claudeDir, "Local Storage/leveldb"), 0700); err != nil {
		t.Fatal(err)
	}

	cfgContent := `{
  "lastKnownAccountUuid": "uuid-1234",
  "oauth:tokenCache": "encrypted-cache-v1",
  "oauth:tokenCacheV2": "encrypted-cache-v2",
  "dxt:allowlistEnabled:org-1": true,
  "locale": "en-US"
}`
	if err := os.WriteFile(filepath.Join(claudeDir, "config.json"), []byte(cfgContent), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(claudeDir, "Cookies"), []byte("sqlite-cookie-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "Local Storage/leveldb/000001.ldb"), []byte("leveldb-fixture"), 0600); err != nil {
		t.Fatal(err)
	}

	// MCP config must remain untouched by profiles
	mcpContent := `{"mcpServers":{"test":{"command":"node"}}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "claude_desktop_config.json"), []byte(mcpContent), 0600); err != nil {
		t.Fatal(err)
	}

	claudeJSON := `{
  "userID": "user-456",
  "oauthAccount": {
    "accountUuid": "uuid-1234",
    "emailAddress": "test@example.com",
    "displayName": "Test User",
    "organizationType": "claude_pro"
  },
  "projects": ["p1"]
}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(claudeJSON), 0600); err != nil {
		t.Fatal(err)
	}

	return home
}

func TestSaveAndListClaudeProfiles(t *testing.T) {
	home := setupTestClaudeEnvironment(t)

	profiles, err := ListProfiles()
	if err != nil {
		t.Fatalf("ListProfiles error: %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("expected 0 profiles initially, got %d", len(profiles))
	}

	if err := SaveProfile("work"); err != nil {
		t.Fatalf("SaveProfile failed: %v", err)
	}

	profiles, err = ListProfiles()
	if err != nil {
		t.Fatalf("ListProfiles after save error: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	if profiles[0].Name != "work" || profiles[0].Email != "test@example.com" {
		t.Fatalf("unexpected profile info: %+v", profiles[0])
	}

	// Verify permissions
	profDir := filepath.Join(home, ".local/share/claude-profiles/work")
	fi, err := os.Stat(profDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0700 {
		t.Fatalf("expected profile dir perm 0700, got %o", perm)
	}

	metaFi, err := os.Stat(filepath.Join(profDir, "profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := metaFi.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected profile.json perm 0600, got %o", perm)
	}

	// Verify desktop shortcut created
	desktopFile := filepath.Join(home, "Desktop/claude-work.desktop")
	if _, err := os.Stat(desktopFile); err != nil {
		t.Fatalf("desktop shortcut missing: %v", err)
	}

	// Verify MCP config was untouched
	mcpData, err := os.ReadFile(filepath.Join(home, ".config/Claude/claude_desktop_config.json"))
	if err != nil || len(mcpData) == 0 {
		t.Fatalf("claude_desktop_config.json damaged: %v", err)
	}
}

func TestSwitchClaudeProfile(t *testing.T) {
	home := setupTestClaudeEnvironment(t)

	if err := SaveProfile("prof1"); err != nil {
		t.Fatalf("SaveProfile prof1: %v", err)
	}

	// Update active state to prof2
	cfgContent2 := `{
  "lastKnownAccountUuid": "uuid-9999",
  "oauth:tokenCache": "encrypted-cache-v2",
  "locale": "en-US"
}`
	_ = os.WriteFile(filepath.Join(home, ".config/Claude/config.json"), []byte(cfgContent2), 0600)
	_ = os.WriteFile(filepath.Join(home, ".config/Claude/Cookies"), []byte("sqlite-cookie-prof2"), 0600)

	claudeJSON2 := `{
  "userID": "user-999",
  "oauthAccount": {
    "accountUuid": "uuid-9999",
    "emailAddress": "prof2@example.com",
    "displayName": "User Two"
  },
  "projects": ["p1", "p2"]
}`
	_ = os.WriteFile(filepath.Join(home, ".claude.json"), []byte(claudeJSON2), 0600)

	if err := SaveProfile("prof2"); err != nil {
		t.Fatalf("SaveProfile prof2: %v", err)
	}

	// Now switch back to prof1
	// (Launch will fail gracefully if no real claude executable in test temp dir)
	_ = SwitchProfile("prof1")

	// Verify config.json restored
	cfgBytes, err := os.ReadFile(filepath.Join(home, ".config/Claude/config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfgDoc map[string]any
	if err := json.Unmarshal(cfgBytes, &cfgDoc); err != nil {
		t.Fatal(err)
	}
	if cfgDoc["lastKnownAccountUuid"] != "uuid-1234" {
		t.Fatalf("expected lastKnownAccountUuid uuid-1234, got %v", cfgDoc["lastKnownAccountUuid"])
	}

	// Verify Cookies restored
	cookieData, err := os.ReadFile(filepath.Join(home, ".config/Claude/Cookies"))
	if err != nil || string(cookieData) != "sqlite-cookie-fixture" {
		t.Fatalf("Cookies not restored correctly: %v, content: %s", err, string(cookieData))
	}

	// Verify ~/.claude.json restored oauthAccount while keeping projects
	claudeData, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cDoc struct {
		UserID       string   `json:"userID"`
		Projects     []string `json:"projects"`
		OAuthAccount struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if err := json.Unmarshal(claudeData, &cDoc); err != nil {
		t.Fatal(err)
	}
	if cDoc.OAuthAccount.EmailAddress != "test@example.com" {
		t.Fatalf("expected email test@example.com, got %s", cDoc.OAuthAccount.EmailAddress)
	}
	if len(cDoc.Projects) != 2 {
		t.Fatalf("expected projects preserved, got %+v", cDoc.Projects)
	}
}

func TestFreshClaudeSession(t *testing.T) {
	home := setupTestClaudeEnvironment(t)

	var lastQuery string
	execSqliteFunc = func(cookieDBPath, query string, stdin string) (string, error) {
		lastQuery = query
		return "", nil
	}

	_ = FreshSession()

	if !strings.Contains(lastQuery, "DELETE FROM cookies WHERE name IN") {
		t.Fatalf("expected surgical auth cookie deletion query, got: %s", lastQuery)
	}

	// Cookies file itself remains (preserving Cloudflare clearance and consent preferences)
	if _, err := os.Stat(filepath.Join(home, ".config/Claude/Cookies")); err != nil {
		t.Fatalf("Cookies file should remain intact with non-auth cookies: %v", err)
	}

	cfgBytes, err := os.ReadFile(filepath.Join(home, ".config/Claude/config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfgDoc map[string]any
	if err := json.Unmarshal(cfgBytes, &cfgDoc); err != nil {
		t.Fatal(err)
	}
	if _, exists := cfgDoc["lastKnownAccountUuid"]; exists {
		t.Fatalf("lastKnownAccountUuid should be removed")
	}
	if _, exists := cfgDoc["oauth:tokenCache"]; exists {
		t.Fatalf("oauth:tokenCache should be removed")
	}
	// Non-auth settings must remain
	if cfgDoc["locale"] != "en-US" {
		t.Fatalf("expected non-auth locale to remain, got %v", cfgDoc["locale"])
	}

	// Local Storage must remain untouched
	if _, err := os.Stat(filepath.Join(home, ".config/Claude/Local Storage/leveldb/000001.ldb")); err != nil {
		t.Fatalf("Local Storage should remain intact during fresh session: %v", err)
	}
}

func TestClaudeProfileValidation(t *testing.T) {
	_ = setupTestClaudeEnvironment(t)

	badNames := []string{"", ".", "..", "foo/bar", "foo\\bar", "foo\x00bar"}
	for _, bad := range badNames {
		if err := SaveProfile(bad); err == nil {
			t.Errorf("expected error for bad profile name %q, got nil", bad)
		}
		if err := SwitchProfile(bad); err == nil {
			t.Errorf("expected error for bad switch name %q, got nil", bad)
		}
	}
}

func TestSurgicalCookieOperations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "Cookies")
	if err := os.WriteFile(dbPath, []byte("fake-cookie-db"), 0600); err != nil {
		t.Fatal(err)
	}

	origSqlite := execSqliteFunc
	defer func() { execSqliteFunc = origSqlite }()

	var executedQueries []string
	var executedStdin []string
	execSqliteFunc = func(cookieDBPath, query string, stdin string) (string, error) {
		if query != "" {
			executedQueries = append(executedQueries, query)
		}
		if stdin != "" {
			executedStdin = append(executedStdin, stdin)
		}
		if strings.Contains(query, "SELECT") {
			return "INSERT OR REPLACE INTO cookies VALUES(1, '.claude.ai', '', 'sessionKey', '...', x'7631', ...);", nil
		}
		return "", nil
	}

	// 1. Test extractAuthCookiesSQL
	authSQL, err := extractAuthCookiesSQL(dbPath)
	if err != nil {
		t.Fatalf("extractAuthCookiesSQL: %v", err)
	}
	if !strings.Contains(authSQL, "sessionKey") {
		t.Fatalf("unexpected authSQL: %s", authSQL)
	}
	if len(executedQueries) == 0 || !strings.Contains(executedQueries[0], "WHERE name IN") {
		t.Fatalf("expected SELECT with auth cookie names, got: %+v", executedQueries)
	}

	// 2. Test clearAuthCookies
	executedQueries = nil
	if err := clearAuthCookies(dbPath); err != nil {
		t.Fatalf("clearAuthCookies: %v", err)
	}
	if len(executedQueries) == 0 || !strings.Contains(executedQueries[0], "DELETE FROM cookies WHERE name IN") {
		t.Fatalf("expected DELETE query, got: %+v", executedQueries)
	}

	// 3. Test restoreAuthCookies
	executedQueries = nil
	executedStdin = nil
	if err := restoreAuthCookies(dbPath, authSQL, ""); err != nil {
		t.Fatalf("restoreAuthCookies: %v", err)
	}
	if len(executedQueries) == 0 || !strings.Contains(executedQueries[0], "DELETE FROM cookies WHERE name IN") {
		t.Fatalf("expected DELETE before insert in restoreAuthCookies")
	}
	if len(executedStdin) == 0 || !strings.Contains(executedStdin[0], "sessionKey") {
		t.Fatalf("expected authSQL piped into sqlite via stdin, got: %+v", executedStdin)
	}
}

func TestMergeDeviceRegistry(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "src.json")
	dst := filepath.Join(tmpDir, "dst.json")
	_ = os.WriteFile(src, []byte(`{"uuid-1": "token-1", "uuid-2": "token-2"}`), 0600)
	_ = os.WriteFile(dst, []byte(`{"uuid-0": "token-0", "uuid-1": "old-token"}`), 0600)

	if err := mergeDeviceRegistry(src, dst); err != nil {
		t.Fatalf("mergeDeviceRegistry: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]string
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["uuid-0"] != "token-0" || doc["uuid-1"] != "token-1" || doc["uuid-2"] != "token-2" {
		t.Fatalf("unexpected merged doc: %+v", doc)
	}
}

// A switch that cannot stop the application must not write: Claude Desktop
// holds its config and cookie database open and flushes its own session back
// over whatever was restored.
func TestSwitchProfileRefusesWhileClaudeIsRunning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	profDir := filepath.Join(home, ".local/share/claude-profiles", "work")
	if err := os.MkdirAll(profDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profDir, "profile.json"), []byte(`{"name":"work"}`), 0600); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	origStop, origLaunch := stopClaudeFunc, launchClaudeFunc
	launched := false
	stopClaudeFunc = func() bool { return false }
	launchClaudeFunc = func() error { launched = true; return nil }
	t.Cleanup(func() { stopClaudeFunc, launchClaudeFunc = origStop, origLaunch })

	if err := SwitchProfile("work"); err == nil {
		t.Fatal("expected SwitchProfile to refuse while claude is running")
	}
	if launched {
		t.Fatal("SwitchProfile relaunched claude after refusing to switch")
	}
}

func TestFreshSessionRefusesWhileClaudeIsRunning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	origStop, origLaunch := stopClaudeFunc, launchClaudeFunc
	launched := false
	stopClaudeFunc = func() bool { return false }
	launchClaudeFunc = func() error { launched = true; return nil }
	t.Cleanup(func() { stopClaudeFunc, launchClaudeFunc = origStop, origLaunch })

	if err := FreshSession(); err == nil {
		t.Fatal("expected FreshSession to refuse while claude is running")
	}
	if launched {
		t.Fatal("FreshSession relaunched claude after refusing to clear the session")
	}
}
