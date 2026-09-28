package antigravity

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"agentcli.local/ai/internal/fsutil"
	"agentcli.local/ai/internal/sqlite"
)

var saveKeys = []string{
	"antigravityUnifiedStateSync.oauthToken",
	"antigravityUnifiedStateSync.userStatus",
	"antigravity.profileUrl",
}

type ProfileInfo struct {
	Name       string            `json:"name"`
	Path       string            `json:"path"`
	HasToken   bool              `json:"has_token"`
	UserStatus string            `json:"user_status,omitempty"`
	ModifiedAt time.Time         `json:"modified_at"`
	DBKeys     map[string]string `json:"db_keys,omitempty"`
}

func ProfilesDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local/share/antigravity-profiles")
}

func ListProfiles() ([]ProfileInfo, error) {
	dir := ProfilesDir()
	var profiles []ProfileInfo
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return profiles, nil
		}
		return nil, err
	}

	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			name := strings.TrimSuffix(e.Name(), ".json")
			p := filepath.Join(dir, e.Name())
			fi, _ := e.Info()
			modTime := time.Now()
			if fi != nil {
				modTime = fi.ModTime()
			}

			info := ProfileInfo{
				Name:       name,
				Path:       p,
				ModifiedAt: modTime,
				DBKeys:     make(map[string]string),
			}

			if data, err := os.ReadFile(p); err == nil {
				var doc struct {
					DB map[string]string `json:"db"`
				}
				if err := json.Unmarshal(data, &doc); err == nil {
					info.DBKeys = doc.DB
					if doc.DB["antigravityUnifiedStateSync.oauthToken"] != "" {
						info.HasToken = true
					}
					info.UserStatus = doc.DB["antigravityUnifiedStateSync.userStatus"]
				}
			}
			profiles = append(profiles, info)
		}
	}
	return profiles, nil
}

func SaveProfile(profileName string) error {
	if err := fsutil.ValidateName(profileName); err != nil {
		return err
	}
	dir := ProfilesDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	home, _ := os.UserHomeDir()
	dbFile := filepath.Join(home, ".config/Antigravity IDE/User/globalStorage/state.vscdb")
	profileJSONPath := filepath.Join(dir, profileName+".json")

	doc := struct {
		DB map[string]string `json:"db"`
	}{
		DB: make(map[string]string),
	}

	if existing, err := os.ReadFile(profileJSONPath); err == nil {
		_ = json.Unmarshal(existing, &doc)
	}

	// A key the IDE has not written yet is simply absent, but a database that
	// will not open means the saved profile would silently be missing its
	// token — which only shows up later, as a switch that logs in as nobody.
	for _, key := range saveKeys {
		rows, err := sqlite.Scalar(dbFile, "SELECT value FROM ItemTable WHERE key = ?;", key)
		if err != nil {
			return fmt.Errorf("read %s from %s: %w", key, dbFile, err)
		}
		if len(rows) > 0 {
			if v := strings.TrimSpace(rows[0]); v != "" {
				doc.DB[key] = v
			}
		}
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if _, err := os.Stat(profileJSONPath); err == nil {
		if err := os.Chmod(profileJSONPath, 0600); err != nil {
			return err
		}
	}
	if err := os.WriteFile(profileJSONPath, data, 0600); err != nil {
		return err
	}

	// Update Desktop shortcut
	desktopDir := filepath.Join(home, "Desktop")
	_ = os.MkdirAll(desktopDir, 0755)
	desktopFile := filepath.Join(desktopDir, fmt.Sprintf("antigravity-%s.desktop", profileName))
	icon := filepath.Join(home, ".local/share/icons/hicolor/512x512/apps/antigravity-ide.png")
	switcherBin := filepath.Join(home, ".local/bin/antigravity-switcher.sh")

	shortcutContent := fmt.Sprintf(`[Desktop Entry]
Name=Antigravity (%s)
Comment=AI Coding Agent IDE - %s profile
GenericName=Text Editor
Exec=%s --profile %s %%F
Icon=%s
Type=Application
StartupNotify=false
StartupWMClass=Antigravity IDE
Categories=TextEditor;Development;IDE;
Terminal=false
`, profileName, profileName, switcherBin, profileName, icon)

	_ = os.WriteFile(desktopFile, []byte(shortcutContent), 0755)
	return nil
}

func SwitchProfile(profileName string) error {
	if err := fsutil.ValidateName(profileName); err != nil {
		return err
	}
	dir := ProfilesDir()
	profileJSONPath := filepath.Join(dir, profileName+".json")
	data, err := os.ReadFile(profileJSONPath)
	if err != nil {
		return fmt.Errorf("no profile found for %q at %s", profileName, profileJSONPath)
	}

	var doc struct {
		DB map[string]string `json:"db"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("corrupted profile JSON: %w", err)
	}

	StopAntigravity()

	home, _ := os.UserHomeDir()
	dbFile := filepath.Join(home, ".config/Antigravity IDE/User/globalStorage/state.vscdb")

	// One transaction, and the error is returned: writing nothing because the
	// database was locked or sqlite3 was missing used to be reported as a
	// successful switch, after which the IDE relaunched with the old session.
	if err := writeStateKeys(dbFile, doc.DB); err != nil {
		return fmt.Errorf("switch to profile %q: %w", profileName, err)
	}

	// Launch IDE
	return LaunchAntigravity()
}

func FreshSession() error {
	StopAntigravity()

	home, _ := os.UserHomeDir()
	dbFile := filepath.Join(home, ".config/Antigravity IDE/User/globalStorage/state.vscdb")

	if err := deleteStateKeys(dbFile, saveKeys); err != nil {
		return fmt.Errorf("clear the current session: %w", err)
	}

	return LaunchAntigravity()
}

// writeStateKeys replaces the given ItemTable keys in one transaction, so a
// partial write cannot leave one profile's token beside another's user status.
func writeStateKeys(dbFile string, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	// Sorted so a failure is reproducible and the statement is stable.
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var stmt strings.Builder
	stmt.WriteString("BEGIN IMMEDIATE;\n")
	args := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		stmt.WriteString("INSERT OR REPLACE INTO ItemTable (key, value) VALUES (?, ?);\n")
		args = append(args, k, values[k])
	}
	stmt.WriteString("COMMIT;\n")
	return sqlite.Exec(dbFile, stmt.String(), args...)
}

// deleteStateKeys removes the given ItemTable keys in one transaction. A
// partially cleared session is still a signed-in session.
func deleteStateKeys(dbFile string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	var stmt strings.Builder
	stmt.WriteString("BEGIN IMMEDIATE;\n")
	for range keys {
		stmt.WriteString("DELETE FROM ItemTable WHERE key = ?;\n")
	}
	stmt.WriteString("COMMIT;\n")
	return sqlite.Exec(dbFile, stmt.String(), keys...)
}

func StopAntigravity() {
	// 1. Graceful SIGINT (trigger window close & state flush)
	_ = exec.Command("pkill", "-INT", "-f", "antigravity-ide").Run()
	for i := 0; i < 6; i++ {
		time.Sleep(500 * time.Millisecond)
		if err := exec.Command("pgrep", "-f", "antigravity-ide").Run(); err != nil {
			return // All processes exited cleanly
		}
	}

	// 2. SIGTERM if still lingering
	_ = exec.Command("pkill", "-TERM", "-f", "antigravity-ide").Run()
	for i := 0; i < 6; i++ {
		time.Sleep(500 * time.Millisecond)
		if err := exec.Command("pgrep", "-f", "antigravity-ide").Run(); err != nil {
			return // Exited
		}
	}

	// 3. Force kill if still hung
	_ = exec.Command("pkill", "-9", "-f", "antigravity-ide").Run()
	time.Sleep(500 * time.Millisecond)
}

func LaunchAntigravity() error {
	execPath := "/var/home/linuxbrew/.linuxbrew/bin/antigravity-ide"
	if _, err := os.Stat(execPath); err != nil {
		p, err := exec.LookPath("antigravity-ide")
		if err != nil {
			return fmt.Errorf("antigravity-ide executable not found")
		}
		execPath = p
	}

	cmd := exec.Command(execPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
