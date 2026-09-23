package antigravity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTurnsPreservesToolCallsAndResults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	data := `{"step_index":1,"type":"USER_INPUT","content":"<USER_REQUEST>Inspect files</USER_REQUEST>"}
{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-09-16T10:14:15Z","tool_calls":[{"name":"run_command","args":{"CommandLine":"\"ls -lt /example\"","Cwd":"\"/example\"","id":9007199254740993}},{"name":"view_file","args":{"Path":"/example/file"}}]}
{"step_index":3,"source":"MODEL","type":"RUN_COMMAND","content":"command output\nexit code 0"}
{"step_index":4,"source":"SYSTEM","type":"SYSTEM_MESSAGE","content":"system note"}
{"step_index":5,"source":"MODEL","type":"PLANNER_RESPONSE","content":null}
{"step_index":6,"source":"MODEL","type":"PLANNER_RESPONSE","content":"Done","tool_calls":[{"name":"no_args","args":null}]}
{"step_index":7,"source":"MODEL","type":"PLANNER_RESPONSE","content":"null"}
{"step_index":8,"source":"SYSTEM","type":"CHECKPOINT"}
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	turns := readTurns(path, 0)
	if len(turns) != 8 {
		t.Fatalf("expected 8 visible turns, got %#v", turns)
	}
	if turns[0].Content != "Inspect files" || turns[1].Content != "" {
		t.Fatalf("content decoding: %#v", turns[:2])
	}
	if turns[1].ToolCall != "run_command" || turns[2].ToolCall != "view_file" {
		t.Fatalf("lost multiple calls: %#v", turns[1:3])
	}
	if !strings.Contains(string(turns[1].ToolArguments), "9007199254740993") || !json.Valid(turns[1].ToolArguments) {
		t.Fatalf("arguments corrupted: %s", turns[1].ToolArguments)
	}
	if turns[1].Timestamp.Format("15:04:05") != "10:14:15" || turns[2].StepIndex != 2 {
		t.Fatalf("call provenance lost: %#v", turns[1:3])
	}
	if turns[3].Role != "tool" || turns[3].ToolCall != "run_command" || turns[3].Content != "command output\nexit code 0" {
		t.Fatalf("execution not preserved: %#v", turns[3])
	}
	if turns[4].Role != "system" || turns[5].Content != "Done" || turns[6].ToolCall != "no_args" || len(turns[6].ToolArguments) != 0 || turns[7].Content != "null" {
		t.Fatalf("narrative/null handling: %#v", turns[4:])
	}
	limited := readTurns(path, 2)
	if len(limited) != 2 || limited[0].ToolCall != "no_args" || limited[1].Content != "null" {
		t.Fatalf("incorrect tail: %#v", limited)
	}
}

func TestReadTurnsRetainsLongStructuredContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	payload := `{"type":"PLANNER_RESPONSE","content":{"id":9007199254740993,"text":"` + strings.Repeat("x", 70000) + `"}}`
	if err := os.WriteFile(path, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	turns := readTurns(path, 0)
	if len(turns) != 1 || len(turns[0].Content) < 70000 || !strings.Contains(turns[0].Content, "9007199254740993") {
		t.Fatal("structured content truncated or rounded")
	}
}
