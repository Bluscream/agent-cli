package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func fixtureConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMalformedConfigPreserved(t *testing.T) {
	for _, raw := range []string{`{"unrelated":true,`, `null`, `[]`, `{"mcpServers":null}`, `{"mcpServers":{"bad":null}}`} {
		t.Run(raw, func(t *testing.T) {
			path := fixtureConfig(t, raw)
			if _, err := NewManager(path).Add("test", ServerConfig{Command: "echo"}); err == nil {
				t.Fatal("invalid config accepted")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != raw {
				t.Fatalf("input changed: %s, %v", data, err)
			}
		})
	}
}

func TestUnknownConfigFieldsPreserved(t *testing.T) {
	path := fixtureConfig(t, `{"large":9007199254740993,"mcpServers":{"old":{"command":"echo","timeout":60,"transport":"stdio"}}}`)
	if _, err := NewManager(path).Add("new", ServerConfig{Command: "echo"}); err != nil {
		t.Fatal(err)
	}
	doc, err := readFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(doc.Raw["large"]) != "9007199254740993" {
		t.Fatal("large integer changed")
	}
	cfg, err := json.Marshal(doc.MCPServers["old"])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(cfg, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["timeout"]) != "60" || string(fields["transport"]) != `"stdio"` {
		t.Fatalf("fields lost: %s", cfg)
	}
}

func TestLegacyDisabledServer(t *testing.T) {
	for _, action := range []string{"enable", "remove"} {
		t.Run(action, func(t *testing.T) {
			path := fixtureConfig(t, `{"disabled_mcpServers":{"old":{"command":"echo"}}}`)
			m := NewManager(path)
			var err error
			if action == "enable" {
				_, err = m.SetDisabled("old", false)
			} else {
				_, err = m.Remove("old")
			}
			if err != nil {
				t.Fatal(err)
			}
			servers, err := m.List()
			if err != nil {
				t.Fatal(err)
			}
			if action == "remove" && len(servers) != 0 {
				t.Fatalf("server remains: %+v", servers)
			}
			if action == "enable" && (len(servers) != 1 || servers[0].Disabled) {
				t.Fatalf("still disabled: %+v", servers)
			}
		})
	}
}

func TestWriteFailureReported(t *testing.T) {
	parent := fixtureConfig(t, "x")
	if _, err := NewManager(filepath.Join(parent, "config.json")).Add("x", ServerConfig{Command: "echo"}); err == nil {
		t.Fatal("failure reported as success")
	}
}

func TestMalformedLaterConfigPreventsEarlierWrite(t *testing.T) {
	first := fixtureConfig(t, `{}`)
	last := fixtureConfig(t, `invalid`)
	if _, err := NewManager(first, last).Add("x", ServerConfig{Command: "echo"}); err == nil {
		t.Fatal("invalid config accepted")
	}
	data, err := os.ReadFile(first)
	if err != nil || string(data) != "{}" {
		t.Fatalf("earlier file changed: %s, %v", data, err)
	}
}
