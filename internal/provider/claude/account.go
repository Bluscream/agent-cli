package claude

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentcli.local/ai/internal/desktop"
	"agentcli.local/ai/internal/fsutil"
	"agentcli.local/ai/internal/proc"
	"agentcli.local/ai/internal/sqlite"
)

// ProfileInfo represents summary metadata for an installed Claude profile.
type ProfileInfo struct {
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Email       string    `json:"email,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	Plan        string    `json:"plan,omitempty"`
	AccountUUID string    `json:"account_uuid,omitempty"`
	SavedAt     time.Time `json:"saved_at"`
}

// ProfileMetadata represents the stored JSON document inside a profile directory.
type ProfileMetadata struct {
	Name           string          `json:"name"`
	Email          string          `json:"email,omitempty"`
	DisplayName    string          `json:"display_name,omitempty"`
	Plan           string          `json:"plan,omitempty"`
	AccountUUID    string          `json:"account_uuid,omitempty"`
	UserID         string          `json:"user_id,omitempty"`
	OAuthAccount   json.RawMessage `json:"oauth_account,omitempty"`
	ConfigAuth     map[string]any  `json:"config_auth,omitempty"`
	AuthCookiesSQL string          `json:"auth_cookies_sql,omitempty"`
	SavedAt        time.Time       `json:"saved_at"`
}

// ProfilesDir returns the directory where Claude profile snapshots are saved.
func ProfilesDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local/share/claude-profiles")
}

// ListProfiles scans ProfilesDir and returns all saved Claude profiles.
func ListProfiles() ([]ProfileInfo, error) {
	dir := ProfilesDir()
	var profiles []ProfileInfo
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return profiles, nil
		}
		return nil, fmt.Errorf("reading claude profiles directory: %w", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		profFile := filepath.Join(dir, e.Name(), "profile.json")
		data, err := os.ReadFile(profFile)
		if err != nil {
			continue
		}
		var meta ProfileMetadata
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		if meta.Name == "" {
			meta.Name = e.Name()
		}
		profiles = append(profiles, ProfileInfo{
			Name:        meta.Name,
			Path:        profFile,
			Email:       meta.Email,
			DisplayName: meta.DisplayName,
			Plan:        meta.Plan,
			AccountUUID: meta.AccountUUID,
			SavedAt:     meta.SavedAt,
		})
	}
	return profiles, nil
}

// SaveProfile captures the active Claude Desktop session and writes it to a profile directory.
func SaveProfile(profileName string) error {
	if err := fsutil.ValidateName(profileName); err != nil {
		return err
	}
	baseDir := ProfilesDir()
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return fmt.Errorf("creating claude profiles dir: %w", err)
	}

	profDir := filepath.Join(baseDir, profileName)
	if err := os.MkdirAll(profDir, 0700); err != nil {
		return fmt.Errorf("creating profile dir: %w", err)
	}

	home, _ := os.UserHomeDir()
	claudeDataDir := filepath.Join(home, ".config/Claude")
	claudeJSONPath := filepath.Join(home, ".claude.json")
	credsPath := filepath.Join(home, ".claude/.credentials.json")

	meta := ProfileMetadata{
		Name:    profileName,
		SavedAt: time.Now(),
	}

	p := &ClaudeProvider{}
	if active, err := p.GetActiveAccount(); err == nil && active != nil {
		meta.Email = active.Email
		meta.DisplayName = active.DisplayName
		meta.Plan = active.Plan
		meta.AccountUUID = active.ID
	}

	if data, err := os.ReadFile(claudeJSONPath); err == nil {
		var doc struct {
			UserID       string          `json:"userID"`
			OAuthAccount json.RawMessage `json:"oauthAccount"`
		}
		if json.Unmarshal(data, &doc) == nil {
			meta.UserID = doc.UserID
			meta.OAuthAccount = doc.OAuthAccount
		}
	}

	if (meta.Email == "" || meta.Email == "-") && strings.Contains(profileName, "@") {
		meta.Email = profileName
	}
	if meta.DisplayName == "" {
		meta.DisplayName = profileName
	}
	if len(meta.OAuthAccount) == 0 && meta.AccountUUID != "" {
		acctDoc := map[string]string{
			"accountUuid":      meta.AccountUUID,
			"emailAddress":     meta.Email,
			"displayName":      meta.DisplayName,
			"organizationType": meta.Plan,
		}
		meta.OAuthAccount, _ = json.Marshal(acctDoc)
	}

	// Extract auth-specific keys from config.json (preserve user preferences)
	cfgSrc := filepath.Join(claudeDataDir, "config.json")
	if cfgData, err := os.ReadFile(cfgSrc); err == nil {
		var cfgDoc map[string]any
		if json.Unmarshal(cfgData, &cfgDoc) == nil {
			meta.ConfigAuth = extractConfigAuth(cfgDoc)
		}
	}

	// Extract surgical auth cookies SQL if sqlite3 is available
	cookieSrc := filepath.Join(claudeDataDir, "Cookies")
	if authSQL, err := extractAuthCookiesSQL(cookieSrc); err == nil && authSQL != "" {
		meta.AuthCookiesSQL = authSQL
		_ = os.WriteFile(filepath.Join(profDir, "auth_cookies.sql"), []byte(authSQL+"\n"), 0600)
	}

	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling profile metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(profDir, "profile.json"), metaBytes, 0600); err != nil {
		return fmt.Errorf("writing profile.json: %w", err)
	}

	// Also keep a copy of config.json for complete fidelity
	if _, err := os.Stat(cfgSrc); err == nil {
		if err := copyFile(cfgSrc, filepath.Join(profDir, "config.json"), 0600); err != nil {
			return fmt.Errorf("saving config.json: %w", err)
		}
	}

	// Keep a fallback copy of Cookies for first-time profile restores
	if _, err := os.Stat(cookieSrc); err == nil {
		if err := copyFile(cookieSrc, filepath.Join(profDir, "Cookies"), 0600); err != nil {
			return fmt.Errorf("saving Cookies: %w", err)
		}
	}

	// 3. Copy ant-device-registry.json if present
	devRegSrc := filepath.Join(claudeDataDir, "ant-device-registry.json")
	if _, err := os.Stat(devRegSrc); err == nil {
		if err := copyFile(devRegSrc, filepath.Join(profDir, "ant-device-registry.json"), 0600); err != nil {
			return fmt.Errorf("saving ant-device-registry.json: %w", err)
		}
	}

	// 4. Copy credentials.json if present
	if _, err := os.Stat(credsPath); err == nil {
		if err := copyFile(credsPath, filepath.Join(profDir, "credentials.json"), 0600); err != nil {
			return fmt.Errorf("saving credentials.json: %w", err)
		}
	}

	// Note: Local Storage is intentionally NOT saved. UI layout, theme, and unsent drafts remain intact.

	return createDesktopShortcut(home, profileName)
}

var (
	stopClaudeFunc   = StopClaude
	launchClaudeFunc = LaunchClaude
	execSqliteFunc   = defaultExecSqlite
)

// SwitchProfile gracefully stops Claude Desktop, restores the specified profile, and relaunches Claude.
func SwitchProfile(profileName string) error {
	if err := fsutil.ValidateName(profileName); err != nil {
		return err
	}
	baseDir := ProfilesDir()
	profDir := filepath.Join(baseDir, profileName)
	metaFile := filepath.Join(profDir, "profile.json")

	metaBytes, err := os.ReadFile(metaFile)
	if err != nil {
		return fmt.Errorf("no profile found for %q at %s: %w", profileName, profDir, err)
	}
	var meta ProfileMetadata
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return fmt.Errorf("corrupted profile metadata for %q: %w", profileName, err)
	}

	// A still-running Claude Desktop holds these files open and will write its
	// own state back over the restored profile, so refusing here is the only
	// way the switch cannot silently half-apply.
	if !stopClaudeFunc() {
		return fmt.Errorf("claude desktop is still running; not overwriting its session data")
	}

	home, _ := os.UserHomeDir()
	claudeDataDir := filepath.Join(home, ".config/Claude")
	claudeJSONPath := filepath.Join(home, ".claude.json")
	credsPath := filepath.Join(home, ".claude/.credentials.json")

	// Ensure destination directory exists
	if err := os.MkdirAll(claudeDataDir, 0700); err != nil {
		return fmt.Errorf("creating claude data dir: %w", err)
	}

	// Remove stale runtime locks and journals
	cleanStaleLocks(claudeDataDir)

	// 1. Restore auth keys into config.json (surgical key-level update)
	cfgTarget := filepath.Join(claudeDataDir, "config.json")
	if len(meta.ConfigAuth) > 0 {
		if err := restoreConfigJSON(cfgTarget, meta.ConfigAuth); err != nil {
			return fmt.Errorf("updating config.json auth: %w", err)
		}
	} else {
		cfgSaved := filepath.Join(profDir, "config.json")
		if _, err := os.Stat(cfgSaved); err == nil {
			if err := copyFile(cfgSaved, cfgTarget, 0600); err != nil {
				return fmt.Errorf("restoring config.json: %w", err)
			}
		}
	}

	// 2. Restore Cookies surgically (only auth cookies swapped; Cloudflare/consent/theme preserved)
	cookieTarget := filepath.Join(claudeDataDir, "Cookies")
	cookieSaved := filepath.Join(profDir, "Cookies")
	if err := restoreAuthCookies(cookieTarget, meta.AuthCookiesSQL, cookieSaved); err != nil {
		return fmt.Errorf("restoring Cookies: %w", err)
	}

	// 3. Merge ant-device-registry.json (preserves device tokens for all known accounts)
	devRegSaved := filepath.Join(profDir, "ant-device-registry.json")
	devRegTarget := filepath.Join(claudeDataDir, "ant-device-registry.json")
	if _, err := os.Stat(devRegSaved); err == nil {
		_ = mergeDeviceRegistry(devRegSaved, devRegTarget)
	}

	// 4. Restore oauthAccount and userID in ~/.claude.json without erasing other settings
	if err := restoreClaudeJSON(claudeJSONPath, meta); err != nil {
		return fmt.Errorf("updating claude.json: %w", err)
	}

	// 5. Restore credentials.json
	credsSaved := filepath.Join(profDir, "credentials.json")
	if _, err := os.Stat(credsSaved); err == nil {
		_ = os.MkdirAll(filepath.Dir(credsPath), 0700)
		if err := copyFile(credsSaved, credsPath, 0600); err != nil {
			return fmt.Errorf("restoring credentials.json: %w", err)
		}
	} else {
		_ = os.Remove(credsPath)
	}

	// Note: Local Storage is intentionally untouched. UI state and drafts remain intact.

	return launchClaudeFunc()
}

// FreshSession terminates Claude Desktop and wipes auth/session files so the next start is unauthenticated.
func FreshSession() error {
	if !stopClaudeFunc() {
		return fmt.Errorf("claude desktop is still running; not clearing its session data")
	}

	home, _ := os.UserHomeDir()
	claudeDataDir := filepath.Join(home, ".config/Claude")
	claudeJSONPath := filepath.Join(home, ".claude.json")
	credsPath := filepath.Join(home, ".claude/.credentials.json")

	cleanStaleLocks(claudeDataDir)
	_ = clearAuthCookies(filepath.Join(claudeDataDir, "Cookies"))
	_ = os.Remove(credsPath)

	// Strip auth keys from config.json
	cfgPath := filepath.Join(claudeDataDir, "config.json")
	if cfgData, err := os.ReadFile(cfgPath); err == nil {
		var cfgDoc map[string]any
		if json.Unmarshal(cfgData, &cfgDoc) == nil {
			stripConfigAuth(cfgDoc)
			if updated, err := json.MarshalIndent(cfgDoc, "", "  "); err == nil {
				_ = os.WriteFile(cfgPath, updated, 0600)
			}
		}
	}

	// Strip oauthAccount from ~/.claude.json
	if claudeData, err := os.ReadFile(claudeJSONPath); err == nil {
		var doc map[string]json.RawMessage
		if json.Unmarshal(claudeData, &doc) == nil {
			delete(doc, "oauthAccount")
			if updated, err := json.MarshalIndent(doc, "", "  "); err == nil {
				_ = os.WriteFile(claudeJSONPath, updated, 0600)
			}
		}
	}

	// Note: Local Storage is untouched.

	return launchClaudeFunc()
}

// claudeProcessPattern matches Claude Desktop, however it was installed.
const claudeProcessPattern = `claude\.appimage|claude-desktop`

// StopClaude gracefully terminates Claude Desktop processes owned by the
// current user, and reports whether they are gone.
func StopClaude() bool {
	return proc.Stop(claudeProcessPattern)
}

// LaunchClaude starts the Claude Desktop application detached from the current process.
func LaunchClaude() error {
	home, _ := os.UserHomeDir()
	return proc.Launch("claude",
		[]string{
			filepath.Join(home, "AppImages/claude.appimage"),
			filepath.Join(home, ".local/bin/Claude_Desktop.AppImage"),
		},
		[]string{"claude-desktop", "claude"},
		"DESKTOPINTEGRATION=1",
	)
}

func cleanStaleLocks(dir string) {
	_ = os.Remove(filepath.Join(dir, "SingletonLock"))
	_ = os.Remove(filepath.Join(dir, "SingletonCookie"))
	_ = os.Remove(filepath.Join(dir, "SingletonSocket"))
	_ = os.Remove(filepath.Join(dir, "Cookies-journal"))
	_ = os.Remove(filepath.Join(dir, "Cookies-wal"))
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Chmod(mode)
}

func restoreClaudeJSON(path string, meta ProfileMetadata) error {
	var doc map[string]json.RawMessage
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &doc)
	}
	if doc == nil {
		doc = make(map[string]json.RawMessage)
	}

	if len(meta.OAuthAccount) > 0 {
		doc["oauthAccount"] = meta.OAuthAccount
	} else if meta.Email != "" || meta.AccountUUID != "" {
		acct := map[string]string{
			"accountUuid":  meta.AccountUUID,
			"emailAddress": meta.Email,
			"displayName":  meta.DisplayName,
		}
		if b, err := json.Marshal(acct); err == nil {
			doc["oauthAccount"] = b
		}
	}

	if meta.UserID != "" {
		if b, err := json.Marshal(meta.UserID); err == nil {
			doc["userID"] = b
		}
	}

	updated, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, updated, 0600)
}

func restoreConfigJSON(path string, authKeys map[string]any) error {
	var doc map[string]any
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &doc)
	}
	if doc == nil {
		doc = make(map[string]any)
	}
	stripConfigAuth(doc)
	for k, v := range authKeys {
		doc[k] = v
	}
	updated, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, updated, 0600)
}

func createDesktopShortcut(home, profileName string) error {
	icon := filepath.Join(home, "AppImages/.icons/claude")
	if _, err := os.Stat(icon); err != nil {
		icon = filepath.Join(home, ".local/share/icons/hicolor/512x512/apps/claude.png")
	}

	aiBin := filepath.Join(home, ".local/bin/ai")
	if _, err := os.Stat(aiBin); err != nil {
		if p, err := exec.LookPath("ai"); err == nil {
			aiBin = p
		}
	}

	return desktop.Entry{
		FileName:   "claude-" + profileName,
		Name:       fmt.Sprintf("Claude (%s)", profileName),
		Comment:    fmt.Sprintf("Claude Desktop - %s profile", profileName),
		Exec:       []string{aiBin, "account", "switch", profileName, "-p", "claude"},
		Icon:       icon,
		WMClass:    "com.anthropic.Claude",
		Categories: "Network;Utility;Development;",
	}.WriteToDesktop()
}

// configAuthKeys are the config.json keys that carry a signed-in session.
// They are named once because the three places that use this set must agree:
// a key extracted into a saved profile but not stripped leaves a live token
// behind on a session that reports itself as fresh.
var configAuthKeys = []string{
	"lastKnownAccountUuid",
	"oauth:tokenCache",
	"oauth:tokenCacheV2",
	"windowSizeWasSignedIn",
}

// configAuthPrefixes are key prefixes whose every match is auth state.
var configAuthPrefixes = []string{"dxt:allowlist"}

// isConfigAuthKey reports whether a config.json key holds session state.
func isConfigAuthKey(key string) bool {
	for _, k := range configAuthKeys {
		if key == k {
			return true
		}
	}
	for _, p := range configAuthPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// extractConfigAuth copies the auth keys out of a decoded config.json.
func extractConfigAuth(doc map[string]any) map[string]any {
	out := make(map[string]any)
	for k, v := range doc {
		if isConfigAuthKey(k) {
			out[k] = v
		}
	}
	return out
}

// stripConfigAuth removes every auth key from a decoded config.json, leaving
// the user's preferences in place.
func stripConfigAuth(doc map[string]any) {
	for k := range doc {
		if isConfigAuthKey(k) {
			delete(doc, k)
		}
	}
}

var authCookieNames = []string{
	"sessionKey",
	"sessionKeyV3",
	"sessionKeyLC",
	"sessionKeyV3LC",
	"__Host-ant_trusted_device",
	"lastActiveOrg",
	"routingHint",
	"activitySessionId",
}

// defaultExecSqlite runs a statement against the cookie database. It stays a
// package-level variable so tests can inject a fake, but the binary discovery
// it used to carry now lives in internal/sqlite, as the project's single
// resolution policy.
func defaultExecSqlite(cookieDBPath, query string, stdin string) (string, error) {
	stmt := query
	if stmt == "" {
		stmt = stdin
	}
	return sqlite.Text(cookieDBPath, stmt)
}

// authCookieNameList renders authCookieNames for an IN clause. Built once: the
// three callers previously each joined the list themselves, so a name added to
// the slice could be extracted and then not cleared.
func authCookieNameList() string {
	quoted := make([]string, 0, len(authCookieNames))
	for _, n := range authCookieNames {
		quoted = append(quoted, sqlite.Literal(n))
	}
	return strings.Join(quoted, ",")
}

func extractAuthCookiesSQL(cookieDBPath string) (string, error) {
	if _, err := os.Stat(cookieDBPath); err != nil {
		return "", err
	}
	namesList := authCookieNameList()
	query := fmt.Sprintf(`SELECT 'INSERT OR REPLACE INTO cookies VALUES(' || creation_utc || ',''' || host_key || ''',''' || top_frame_site_key || ''',''' || name || ''',''' || value || ''',x''' || hex(encrypted_value) || ''',''' || path || ''',' || expires_utc || ',' || is_secure || ',' || is_httponly || ',' || last_access_utc || ',' || has_expires || ',' || is_persistent || ',' || priority || ',' || samesite || ',' || source_scheme || ',' || source_port || ',' || last_update_utc || ',' || source_type || ',' || has_cross_site_ancestor || ');' FROM cookies WHERE name IN (%s);`, namesList)
	return execSqliteFunc(cookieDBPath, query, "")
}

func restoreAuthCookies(cookieDBPath, authSQL, fallbackCookiePath string) error {
	namesList := authCookieNameList()
	if authSQL != "" {
		if _, statErr := os.Stat(cookieDBPath); statErr == nil {
			deleteQuery := fmt.Sprintf("DELETE FROM cookies WHERE name IN (%s);", namesList)
			_, _ = execSqliteFunc(cookieDBPath, deleteQuery, "")
			if _, err := execSqliteFunc(cookieDBPath, "", authSQL+"\n"); err == nil {
				return nil
			}
		}
	}
	// Fallback to file copy if sqlite not available or destination DB didn't exist
	if fallbackCookiePath != "" {
		if _, statErr := os.Stat(fallbackCookiePath); statErr == nil {
			return copyFile(fallbackCookiePath, cookieDBPath, 0600)
		}
	}
	return nil
}

func clearAuthCookies(cookieDBPath string) error {
	namesList := authCookieNameList()
	if _, statErr := os.Stat(cookieDBPath); statErr == nil {
		deleteQuery := fmt.Sprintf("DELETE FROM cookies WHERE name IN (%s);", namesList)
		if _, err := execSqliteFunc(cookieDBPath, deleteQuery, ""); err == nil {
			return nil
		}
	}
	// Fallback: remove file if sqlite deletion fails or sqlite not available
	return os.Remove(cookieDBPath)
}

func mergeDeviceRegistry(src, dst string) error {
	srcData, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	var srcMap map[string]any
	if err := json.Unmarshal(srcData, &srcMap); err != nil {
		return err
	}
	dstMap := make(map[string]any)
	if dstData, err := os.ReadFile(dst); err == nil {
		_ = json.Unmarshal(dstData, &dstMap)
	}
	for k, v := range srcMap {
		dstMap[k] = v
	}
	merged, err := json.MarshalIndent(dstMap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(dst, merged, 0600)
}
