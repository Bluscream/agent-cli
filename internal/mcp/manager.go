package mcp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

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

	res := make([]ServerStatus, 0, len(registry))
	for _, v := range registry {
		res = append(res, *v)
	}

	sort.Slice(res, func(i, j int) bool {
		return res[i].Name < res[j].Name
	})

	return res, nil
}

// mutate validates every input before writing any file. Write failures are
// returned with the paths that were already updated.
func (m *Manager) mutate(create bool, change func(*fileDoc) bool) ([]string, error) {
	type pending struct {
		path string
		doc  *fileDoc
	}
	var writes []pending
	for _, path := range m.ConfigFiles {
		doc, err := readFile(path)
		if os.IsNotExist(err) {
			if !create {
				continue
			}
			doc = newFileDoc()
		} else if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if change(doc) {
			writes = append(writes, pending{path, doc})
		}
	}
	updated := []string{}
	var errs []error
	for _, w := range writes {
		if err := writeFile(w.path, w.doc); err != nil {
			errs = append(errs, fmt.Errorf("write %s: %w", w.path, err))
		} else {
			updated = append(updated, w.path)
		}
	}
	return updated, errors.Join(errs...)
}

func (m *Manager) Add(name string, cfg ServerConfig) ([]string, error) {
	if name == "" {
		return nil, fmt.Errorf("server name is required")
	}
	return m.mutate(true, func(doc *fileDoc) bool {
		doc.MCPServers[name] = cfg
		delete(doc.DisabledMCPServers, name)
		return true
	})
}

func (m *Manager) Remove(name string) ([]string, error) {
	return m.mutate(false, func(doc *fileDoc) bool {
		_, active := doc.MCPServers[name]
		_, disabled := doc.DisabledMCPServers[name]
		delete(doc.MCPServers, name)
		delete(doc.DisabledMCPServers, name)
		return active || disabled
	})
}

func (m *Manager) SetDisabled(name string, disabled bool) ([]string, error) {
	return m.mutate(false, func(doc *fileDoc) bool {
		cfg, ok := doc.MCPServers[name]
		if !ok {
			cfg, ok = doc.DisabledMCPServers[name]
		}
		if !ok {
			return false
		}
		cfg.Disabled = disabled
		doc.MCPServers[name] = cfg
		delete(doc.DisabledMCPServers, name)
		return true
	})
}

func (m *Manager) Sync() ([]string, error) {
	merged := make(map[string]ServerConfig)
	for _, path := range m.ConfigFiles {
		doc, err := readFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		for name, cfg := range doc.MCPServers {
			if _, exists := merged[name]; !exists {
				merged[name] = cfg
			}
		}
	}
	return m.mutate(true, func(doc *fileDoc) bool {
		changed := false
		for name, cfg := range merged {
			_, active := doc.MCPServers[name]
			_, disabled := doc.DisabledMCPServers[name]
			if !active && !disabled {
				doc.MCPServers[name] = cfg
				changed = true
			}
		}
		return changed
	})
}
