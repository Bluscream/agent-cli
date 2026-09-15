package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type ServerConfig struct {
	extra    map[string]json.RawMessage
	Command  string            `json:"command,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	URL      string            `json:"url,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Disabled bool              `json:"disabled"`
}

// Preserve provider-specific server fields when updating another server.
func (c *ServerConfig) UnmarshalJSON(data []byte) error {
	type known ServerConfig
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("server configuration must be an object")
	}
	var value known
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*c = ServerConfig(value)
	for _, key := range []string{"command", "args", "env", "url", "headers", "disabled"} {
		delete(fields, key)
	}
	c.extra = fields
	return nil
}

func (c ServerConfig) MarshalJSON() ([]byte, error) {
	type known ServerConfig
	data, err := json.Marshal(known(c))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for key, value := range c.extra {
		fields[key] = value
	}
	return json.Marshal(fields)
}

type fileDoc struct {
	MCPServers         map[string]ServerConfig
	DisabledMCPServers map[string]ServerConfig
	Raw                map[string]json.RawMessage
}

func newFileDoc() *fileDoc {
	return &fileDoc{MCPServers: map[string]ServerConfig{}, DisabledMCPServers: map[string]ServerConfig{}, Raw: map[string]json.RawMessage{}}
}

func readFile(path string) (*fileDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc := newFileDoc()
	if err := json.Unmarshal(data, &doc.Raw); err != nil {
		return nil, err
	}
	if doc.Raw == nil {
		return nil, fmt.Errorf("configuration must be an object")
	}
	for key, target := range map[string]*map[string]ServerConfig{"mcpServers": &doc.MCPServers, "disabled_mcpServers": &doc.DisabledMCPServers} {
		if raw, ok := doc.Raw[key]; ok {
			if err := json.Unmarshal(raw, target); err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			if *target == nil {
				return nil, fmt.Errorf("%s must be an object", key)
			}
		}
	}
	for name, cfg := range doc.DisabledMCPServers {
		cfg.Disabled = true
		doc.DisabledMCPServers[name] = cfg
	}
	return doc, nil
}

func writeFile(path string, doc *fileDoc) error {
	var err error
	doc.Raw["mcpServers"], err = json.Marshal(doc.MCPServers)
	if err != nil {
		return err
	}
	delete(doc.Raw, "disabled_mcpServers")
	if len(doc.DisabledMCPServers) > 0 {
		doc.Raw["disabled_mcpServers"], err = json.Marshal(doc.DisabledMCPServers)
		if err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(doc.Raw, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	mode := os.FileMode(0600)
	if st, err := os.Lstat(path); err == nil {
		if !st.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular file %s", path)
		}
		mode = st.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := os.CreateTemp(dir, ".ai-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
