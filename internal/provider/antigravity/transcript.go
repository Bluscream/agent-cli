package antigravity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"time"

	"agentcli.local/ai/internal/provider"
)

func transcriptContent(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	// Preserve structured content without converting large JSON numbers to floats.
	return string(raw)
}

func readTurns(trPath string, limit int) []provider.TurnInfo {
	var turns []provider.TurnInfo
	f, err := os.Open(trPath)
	if err != nil {
		return turns
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		var d struct {
			StepIndex int             `json:"step_index"`
			Source    string          `json:"source"`
			Type      string          `json:"type"`
			Content   json.RawMessage `json:"content"`
			CreatedAt string          `json:"created_at"`
			Thinking  string          `json:"thinking"`
			ToolCalls []struct {
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"tool_calls"`
		}
		if json.Unmarshal(scanner.Bytes(), &d) != nil {
			continue
		}

		turn := provider.TurnInfo{
			StepIndex: d.StepIndex,
			Role:      "system",
			Content:   strings.TrimSpace(transcriptContent(d.Content)),
			Thinking:  strings.TrimSpace(d.Thinking),
		}
		switch {
		case d.Type == "USER_INPUT":
			turn.Role = "user"
		case d.Type == "PLANNER_RESPONSE" || d.Type == "MODEL":
			turn.Role = "assistant"
		case d.Source == "MODEL":
			// Antigravity exports executions (RUN_COMMAND, VIEW_FILE, etc.)
			// as model-sourced steps, separately from the planner's calls.
			turn.Role = "tool"
			turn.ToolCall = strings.ToLower(d.Type)
		}
		if turn.Role == "user" && strings.Contains(turn.Content, "<USER_REQUEST>") {
			parts := strings.SplitN(turn.Content, "<USER_REQUEST>", 2)
			turn.Content = strings.TrimSpace(strings.SplitN(parts[1], "</USER_REQUEST>", 2)[0])
		}
		turn.Timestamp, _ = time.Parse(time.RFC3339Nano, d.CreatedAt)
		if turn.Content != "" || turn.Thinking != "" || turn.ToolCall != "" {
			turns = append(turns, turn)
		}

		// Keep narrative and each call separate so --tools can hide calls without
		// dropping assistant text, and multiple calls retain all arguments.
		for _, call := range d.ToolCalls {
			args := call.Args
			if bytes.Equal(bytes.TrimSpace(args), []byte("null")) {
				args = nil
			}
			turns = append(turns, provider.TurnInfo{
				StepIndex:     d.StepIndex,
				Role:          "assistant",
				Timestamp:     turn.Timestamp,
				ToolCall:      call.Name,
				ToolArguments: args,
			})
		}
	}
	if limit > 0 && len(turns) > limit {
		turns = turns[len(turns)-limit:]
	}
	return turns
}
