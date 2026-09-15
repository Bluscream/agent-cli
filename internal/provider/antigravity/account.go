package antigravity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
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
	if profileName == "" {
		return fmt.Errorf("profile name is required")
	}
	dir := ProfilesDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
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

	for _, key := range saveKeys {
		cmd := exec.Command("sqlite3", dbFile, fmt.Sprintf("SELECT value FROM ItemTable WHERE key='%s';", key))
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil {
			v := strings.TrimSpace(out.String())
			if v != "" {
				doc.DB[key] = v
			}
		}
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(profileJSONPath, data, 0644); err != nil {
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

	for k, v := range doc.DB {
		escaped := strings.ReplaceAll(v, "'", "''")
		query := fmt.Sprintf("INSERT OR REPLACE INTO ItemTable (key, value) VALUES ('%s', '%s');", k, escaped)
		_ = exec.Command("sqlite3", dbFile, query).Run()
	}

	// Launch IDE
	return LaunchAntigravity()
}

func FreshSession() error {
	StopAntigravity()

	home, _ := os.UserHomeDir()
	dbFile := filepath.Join(home, ".config/Antigravity IDE/User/globalStorage/state.vscdb")

	for _, key := range saveKeys {
		query := fmt.Sprintf("DELETE FROM ItemTable WHERE key='%s';", key)
		_ = exec.Command("sqlite3", dbFile, query).Run()
	}

	return LaunchAntigravity()
}

func StopAntigravity() {
	// Graceful SIGINT -> SIGTERM -> SIGKILL
	_ = exec.Command("pkill", "-INT", "-f", "antigravity-ide").Run()
	time.Sleep(1 * time.Second)

	cmdCheck := exec.Command("pgrep", "-f", "antigravity-ide")
	if err := cmdCheck.Run(); err == nil {
		_ = exec.Command("pkill", "-TERM", "-f", "antigravity-ide").Run()
		time.Sleep(1 * time.Second)
		if err := exec.Command("pgrep", "-f", "antigravity-ide").Run(); err == nil {
			_ = exec.Command("pkill", "-9", "-f", "antigravity-ide").Run()
		}
	}
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
