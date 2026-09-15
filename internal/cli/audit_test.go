package cli

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestAbsoluteDateAndMinutes(t *testing.T) {
	const stamp = "2026-09-01T00:00:00Z"
	got, err := ParseSinceDuration(stamp)
	if err != nil || got.Format(time.RFC3339) != stamp {
		t.Fatalf("timestamp: %v %v", got, err)
	}
	for _, text := range []string{"1 minute", "2 minutes", "1 min", "2 mins"} {
		if _, err := ParseSinceDuration(text); err != nil {
			t.Errorf("%s: %v", text, err)
		}
	}
}

func TestUnknownProvider(t *testing.T) {
	for _, command := range []string{"models", "limits"} {
		cmd := New(&bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{})
		cmd.SetArgs([]string{command, "--provider", "not-a-provider", "--output=compact"})
		if err := cmd.Execute(); err == nil {
			t.Errorf("%s accepted invalid provider", command)
		}
	}
}

func TestEmptyLastsJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DEBUG", "0")
	t.Setenv("AI_DEBUG", "0")
	out := &bytes.Buffer{}
	cmd := New(&bytes.Buffer{}, out, &bytes.Buffer{})
	cmd.SetArgs([]string{"lasts", "--output=compact"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out.Bytes()) {
		t.Fatalf("invalid JSON: %s", out)
	}
}
