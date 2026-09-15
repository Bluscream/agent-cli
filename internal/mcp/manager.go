package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type ServerConfig struct {
	Command  string            `json:"command,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	URL      string            `json:"url,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Disabled bool              `json:"disabled"`
}

type ServerStatus struct {
	Name     string       `json:"name"`
	Config   ServerConfig `json:"config"`
	Files    []string     `json:"files"`
	Disabled bool         `json:"disabled"`
}

func DefaultConfigFiles() []string {
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(home, ".config/Antigravity IDE/User/settings.json"),
		filepath.Join(home, ".config/VSCodium/User/globalStorage/zoocodeorganization.zoo-code/settings/mcp_settings.json"),
		filepath.Join(home, ".gemini/antigravity-ide/mcp_config.json"),
		filepath.Join(home, ".gemini/config/mcp_config.json"),
		filepath.Join(home, ".config/Claude/claude_desktop_config.json"),
		"/var/mnt/nas/projects/MCPs/mcp.json",
	}
}

type Manager struct {
	ConfigFiles []string
}

func NewManager(customFiles ...string) *Manager {
	if len(customFiles) > 0 {
		return &Manager{ConfigFiles: customFiles}
	}
	return &Manager{ConfigFiles: DefaultConfigFiles()}
}

type fileDoc struct {
	MCPServers         map[string]ServerConfig `json:"mcpServers,omitempty"`
	DisabledMCPServers map[string]ServerConfig `json:"disabled_mcpServers,omitempty"`
	Raw                map[string]any          `json:"-"`
}

func readFile(path string) (*fileDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	doc := &fileDoc{
		MCPServers:         make(map[string]ServerConfig),
		DisabledMCPServers: make(map[string]ServerConfig),
		Raw:                make(map[string]any),
	}

	_ = json.Unmarshal(data, &doc.Raw)

	if mcpRaw, ok := doc.Raw["mcpServers"].(map[string]any); ok {
		for k, v := range mcpRaw {
			b, _ := json.Marshal(v)
			var cfg ServerConfig
			_ = json.Unmarshal(b, &cfg)
			doc.MCPServers[k] = cfg
		}
	}

	if disRaw, ok := doc.Raw["disabled_mcpServers"].(map[string]any); ok {
		for k, v := range disRaw {
			b, _ := json.Marshal(v)
			var cfg ServerConfig
			_ = json.Unmarshal(b, &cfg)
			cfg.Disabled = true
			doc.DisabledMCPServers[k] = cfg
		}
	}

	return doc, nil
}

func writeFile(path string, doc *fileDoc) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	if doc.Raw == nil {
		doc.Raw = make(map[string]any)
	}
	doc.Raw["mcpServers"] = doc.MCPServers
	if len(doc.DisabledMCPServers) > 0 {
		doc.Raw["disabled_mcpServers"] = doc.DisabledMCPServers
	}

	data, err := json.MarshalIndent(doc.Raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (m *Manager) List() ([]ServerStatus, error) {
	registry := make(map[string]*ServerStatus)

	for _, file := range m.ConfigFiles {
		doc, err := readFile(file)
		if err != nil {
			continue
		}

		for name, cfg := range doc.MCPServers {
			st, exists := registry[name]
			if !exists {
				st = &ServerStatus{
					Name:     name,
					Config:   cfg,
					Disabled: cfg.Disabled,
				}
				registry[name] = st
			}
			st.Files = append(st.Files, file)
			if !cfg.Disabled {
				st.Disabled = false
			}
		}

		for name, cfg := range doc.DisabledMCPServers {
			st, exists := registry[name]
			if !exists {
				st = &ServerStatus{
					Name:     name,
					Config:   cfg,
					Disabled: true,
				}
				registry[name] = st
			}
			st.Files = append(st.Files, file)
		}
	}

	var res []ServerStatus
	for _, v := range registry {
		res = append(res, *v)
	}

	sort.Slice(res, func(i, j int) bool {
		return res[i].Name < res[j].Name
	})

	return res, nil
}

func (m *Manager) Add(name string, cfg ServerConfig) ([]string, error) {
	if name == "" {
		return nil, fmt.Errorf("server name is required")
	}

	var updated []string
	for _, file := range m.ConfigFiles {
		doc, err := readFile(file)
		if err != nil {
			if os.IsNotExist(err) {
				doc = &fileDoc{
					MCPServers: make(map[string]ServerConfig),
					Raw:        make(map[string]any),
				}
			} else {
				continue
			}
		}

		doc.MCPServers[name] = cfg
		if err := writeFile(file, doc); err == nil {
			updated = append(updated, file)
		}
	}
	return updated, nil
}

func (m *Manager) Remove(name string) ([]string, error) {
	var updated []string
	for _, file := range m.ConfigFiles {
		doc, err := readFile(file)
		if err != nil {
			continue
		}

		changed := false
		if _, ok := doc.MCPServers[name]; ok {
			delete(doc.MCPServers, name)
			changed = true
		}
		if _, ok := doc.DisabledMCPServers[name]; ok {
			delete(doc.DisabledMCPServers, name)
			changed = true
		}

		if changed {
			if err := writeFile(file, doc); err == nil {
				updated = append(updated, file)
			}
		}
	}
	return updated, nil
}

func (m *Manager) SetDisabled(name string, disabled bool) ([]string, error) {
	var updated []string
	for _, file := range m.ConfigFiles {
		doc, err := readFile(file)
		if err != nil {
			continue
		}

		changed := false
		if cfg, ok := doc.MCPServers[name]; ok {
			cfg.Disabled = disabled
			doc.MCPServers[name] = cfg
			changed = true
		}
		if cfg, ok := doc.DisabledMCPServers[name]; ok {
			cfg.Disabled = disabled
			doc.DisabledMCPServers[name] = cfg
			changed = true
		}

		if changed {
			if err := writeFile(file, doc); err == nil {
				updated = append(updated, file)
			}
		}
	}
	return updated, nil
}

func (m *Manager) Sync() ([]string, error) {
	// Union of all servers
	merged := make(map[string]ServerConfig)
	for _, file := range m.ConfigFiles {
		doc, err := readFile(file)
		if err != nil {
			continue
		}
		for name, cfg := range doc.MCPServers {
			if _, exists := merged[name]; !exists {
				merged[name] = cfg
			}
		}
	}

	var synced []string
	for _, file := range m.ConfigFiles {
		doc, err := readFile(file)
		if err != nil {
			if os.IsNotExist(err) {
				doc = &fileDoc{
					MCPServers: make(map[string]ServerConfig),
					Raw:        make(map[string]any),
				}
			} else {
				continue
			}
		}

		for name, cfg := range merged {
			if _, exists := doc.MCPServers[name]; !exists {
				doc.MCPServers[name] = cfg
			}
		}

		if err := writeFile(file, doc); err == nil {
			synced = append(synced, file)
		}
	}

	return synced, nil
}
