package cli_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"agentcli.local/ai/internal/cli"
)

func TestDebugTimer(t *testing.T) {
	timer := cli.NewTimer(true)
	if !timer.IsEnabled() {
		t.Fatal("expected timer to be enabled when explicitDebug=true")
	}

	time.Sleep(5 * time.Millisecond)
	timer.Step("operation_1")

	time.Sleep(5 * time.Millisecond)
	timer.Step("operation_2")

	metrics := timer.Metrics()
	if len(metrics.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(metrics.Steps))
	}
	if metrics.Steps[0].Name != "operation_1" {
		t.Errorf("expected step 1 name 'operation_1', got '%s'", metrics.Steps[0].Name)
	}
	if metrics.Steps[1].Name != "operation_2" {
		t.Errorf("expected step 2 name 'operation_2', got '%s'", metrics.Steps[1].Name)
	}
	if metrics.Steps[0].Nanos <= 0 || metrics.Steps[1].Nanos <= 0 {
		t.Errorf("expected positive step nanos")
	}

	data, err := json.Marshal(metrics)
	if err != nil {
		t.Fatalf("failed to marshal metrics: %v", err)
	}
	if !bytes.Contains(data, []byte("operation_1")) {
		t.Errorf("json missing operation_1: %s", string(data))
	}
}
