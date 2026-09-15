package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMCPManagerAddRemoveSync(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mcp-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	file1 := filepath.Join(tempDir, "config1.json")
	file2 := filepath.Join(tempDir, "config2.json")

	mgr := NewManager(file1, file2)

	// 1. Add server
	cfg := ServerConfig{
		Command: "node",
		Args:    []string{"server.js"},
	}
	updated, err := mgr.Add("test-server", cfg)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if len(updated) != 2 {
		t.Errorf("expected 2 files updated, got %d", len(updated))
	}

	// 2. List servers
	list, err := mgr.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(list) != 1 || list[0].Name != "test-server" {
		t.Errorf("expected test-server in list, got %v", list)
	}

	// 3. Disable server
	_, err = mgr.SetDisabled("test-server", true)
	if err != nil {
		t.Fatalf("SetDisabled failed: %v", err)
	}
	list, _ = mgr.List()
	if len(list) == 0 || !list[0].Disabled {
		t.Errorf("expected server to be disabled")
	}

	// 4. Remove server
	_, err = mgr.Remove("test-server")
	if err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	list, _ = mgr.List()
	if len(list) != 0 {
		t.Errorf("expected empty list after remove, got %v", list)
	}
}
