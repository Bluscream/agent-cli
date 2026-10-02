package ingest

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"agentcli.local/ai/internal/provider"
)

// Payload is one transcript turn as stored in Qdrant. Field names match the
// schema the retired TypeScript daemon wrote, so existing search tooling can be
// repointed at this collection without changing its queries.
type Payload struct {
	SessionID   string `json:"session_id"`
	Provider    string `json:"provider"`
	Hostname    string `json:"hostname"`
	ProjectPath string `json:"project_path"`
	Title       string `json:"title,omitempty"`
	Role        string `json:"role"`
	Content     string `json:"content"`
	Thinking    string `json:"thinking,omitempty"`
	ToolName    string `json:"tool_name,omitempty"`
	StepIndex   int    `json:"step_index"`
	CreatedAt   string `json:"created_at"`
}

// Point is a Qdrant point.
type Point struct {
	ID string `json:"id"`
	// Qdrant rejects a point with no vector field even when the collection
	// declares no vectors; an empty object is the payload-only form. This is
	// not a zero-filled embedding: it occupies no space.
	Vector  struct{} `json:"vector"`
	Payload Payload  `json:"payload"`
}

// pointNamespace keeps generated IDs from colliding with UUIDs minted elsewhere.
const pointNamespace = "agentcli.local/ai/ingest"

// pointID derives a stable RFC 9562 version 8 UUID from the turn's identity.
// The timestamp is deliberately excluded: re-ingesting a turn whose timestamp
// parsed differently must update the existing point rather than duplicate it.
//
// ordinal distinguishes turns that share a step index — Antigravity emits a
// planner step's narrative and each of its tool calls as separate turns under
// one step_index, and keying on the step alone silently overwrote all but the
// last. It is omitted at zero so the common one-turn-per-step identity is
// unchanged, and only the turns that were being lost get new ids.
func pointID(providerName, sessionID string, stepIndex, ordinal int) string {
	identity := fmt.Sprintf("%s|%s|%s|%d", pointNamespace, providerName, sessionID, stepIndex)
	if ordinal > 0 {
		identity = fmt.Sprintf("%s#%d", identity, ordinal)
	}
	sum := sha256.Sum256([]byte(identity))

	var id [16]byte
	copy(id[:], sum[:16])
	id[6] = (id[6] & 0x0f) | 0x80 // version 8: custom
	id[8] = (id[8] & 0x3f) | 0x80 // RFC 9562 variant

	return fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
}

// turnsToPoints converts a conversation's turns into points, skipping turns
// that carry no text at all. If a redactor is provided, sensitive patterns,
// credentials, and real names are automatically scrubbed from the payload.
func turnsToPoints(cfg *Config, summary provider.ConversationSummary, turns []provider.TurnInfo, redactor ...*Redactor) []Point {
	points := make([]Point, 0, len(turns))
	// Counted only over turns that become points, so an ordinal always lines up
	// with what is stored rather than with turns that were skipped.
	perStep := make(map[int]int)

	var r *Redactor
	if len(redactor) > 0 {
		r = redactor[0]
	}

	title := summary.Title
	projectPath := provider.ParseWorkspace(summary.WorkspaceDir).Path()
	if r != nil {
		title = r.Redact(title)
		projectPath = r.Redact(projectPath)
	}

	for _, turn := range turns {
		content := strings.TrimSpace(turn.Content)
		thinking := strings.TrimSpace(turn.Thinking)
		if content == "" && thinking == "" {
			continue
		}
		if r != nil {
			content = r.Redact(content)
			thinking = r.Redact(thinking)
		}
		ordinal := perStep[turn.StepIndex]
		perStep[turn.StepIndex]++

		role := turn.Role
		if role == "" {
			role = "assistant"
		}

		points = append(points, Point{
			ID: pointID(summary.Provider, summary.ID, turn.StepIndex, ordinal),
			Payload: Payload{
				SessionID:   summary.ID,
				Provider:    summary.Provider,
				Hostname:    cfg.Hostname,
				ProjectPath: projectPath,
				Title:       title,
				Role:        role,
				Content:     content,
				Thinking:    thinking,
				ToolName:    turn.ToolCall,
				StepIndex:   turn.StepIndex,
				CreatedAt:   timestamp(turn.Timestamp, summary.UpdatedAt),
			},
		})
	}
	return points
}

// timestamp formats a turn's time, falling back to the conversation's last
// activity when the transcript carried no usable one.
func timestamp(turnTime, fallback time.Time) string {
	if !turnTime.IsZero() {
		return turnTime.UTC().Format(time.RFC3339)
	}
	if !fallback.IsZero() {
		return fallback.UTC().Format(time.RFC3339)
	}
	return time.Now().UTC().Format(time.RFC3339)
}
