package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentcli.local/ai/internal/provider"
	_ "agentcli.local/ai/internal/provider/antigravity"
)

func TestLogAntigravityToolVisibility(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DEBUG", "0")
	t.Setenv("AI_DEBUG", "0")
	const id = "12345678-1234-1234-1234-123456789abc"
	path := filepath.Join(os.Getenv("HOME"), ".gemini/antigravity-ide/brain", id, ".system_generated/logs/transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data := `{"step_index":1,"type":"USER_INPUT","content":"Inspect files"}
{"step_index":2,"type":"PLANNER_RESPONSE","source":"MODEL","content":"Checking now","tool_calls":[{"name":"run_command","args":{"CommandLine":"ls -lt /example","Cwd":"/example"}},{"name":"view_file","args":{"Path":"/example/file"}}]}
{"step_index":3,"type":"RUN_COMMAND","source":"MODEL","content":"tool output marker"}
{"step_index":4,"type":"SYSTEM_MESSAGE","source":"SYSTEM","content":"system marker"}
{"step_index":5,"type":"PLANNER_RESPONSE","source":"MODEL","content":null}
{"step_index":6,"type":"PLANNER_RESPONSE","source":"MODEL","content":"Done"}
{"step_index":7,"type":"PLANNER_RESPONSE","source":"MODEL","tool_calls":[{"name":"empty_call"}]}
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		flags  []string
		want   []string
		absent []string
	}{
		{"default", nil, []string{"Checking now", "Done"}, []string{"tool output marker", "system marker", "tool: run_command", "\nnull\n"}},
		{"tools", []string{"--tools"}, []string{"Checking now", "CommandLine", "ls -lt /example", "tool: view_file", "tool output marker", "tool: empty_call"}, []string{"system marker", "\nnull\n"}},
		{"system", []string{"--system"}, []string{"system marker", "Done"}, []string{"tool output marker", "tool: run_command"}},
		{"tail", []string{"--limit=1"}, []string{"Done", "Total Turns     : 1"}, []string{"Checking now", "tool:"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			cmd := New(&bytes.Buffer{}, out, &bytes.Buffer{})
			cmd.SetArgs(append([]string{"log", id, "--provider=antigravity", "--color=never"}, tc.flags...))
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			for _, text := range tc.want {
				if !strings.Contains(out.String(), text) {
					t.Errorf("missing %q in %s", text, out)
				}
			}
			for _, text := range tc.absent {
				if strings.Contains(out.String(), text) {
					t.Errorf("unexpected %q in %s", text, out)
				}
			}
		})
	}
	out := &bytes.Buffer{}
	cmd := New(&bytes.Buffer{}, out, &bytes.Buffer{})
	cmd.SetArgs([]string{"log", id, "--provider=antigravity", "--tools", "--output=compact"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	// In debug-tagged builds IsDebugBuild=true forces the timer on regardless of
	// env vars, so printJSON wraps the payload: {"_debug":{...},"data":{...}}.
	// Unwrap "data" when present so the assertion works in both build modes.
	raw := out.Bytes()
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err == nil {
		if data, ok := outer["data"]; ok {
			raw = data
		}
	}
	var result struct {
		Turns []provider.TurnInfo `json:"turns"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Turns) != 7 || result.Turns[2].ToolCall != "run_command" || !strings.Contains(string(result.Turns[2].ToolArguments), "CommandLine") {
		t.Fatalf("structured output lost tools: %s", out)
	}
	p, err := provider.Get("antigravity")
	if err != nil {
		t.Fatal(err)
	}
	detail, err := p.GetConversation(id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.LastResponse != "Done" {
		t.Fatalf("tool call replaced handoff response: %q", detail.LastResponse)
	}
}
