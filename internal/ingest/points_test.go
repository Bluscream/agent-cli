package ingest

import (
	"testing"
	"time"

	"agentcli.local/ai/internal/provider"
)

// Antigravity emits a planner step's narrative and each of its tool calls as
// separate turns sharing one step_index. Keying a point only on the step index
// meant every turn but the last was silently overwritten: one real conversation
// stored 1712 of its 1918 turns and reported success.
func TestTurnsSharingAStepIndexEachGetAPoint(t *testing.T) {
	t.Parallel()
	cfg := &Config{Hostname: "test-host", Collection: "c"}
	summary := provider.ConversationSummary{
		Provider: "antigravity",
		ID:       "session-1",
		Title:    "shared steps",
	}
	now := time.Now()

	turns := []provider.TurnInfo{
		{StepIndex: 1, Role: "user", Content: "ask", Timestamp: now},
		{StepIndex: 2, Role: "assistant", Content: "narrative", Timestamp: now},
		{StepIndex: 2, Role: "assistant", Content: "first tool result", Timestamp: now},
		{StepIndex: 2, Role: "assistant", Content: "second tool result", Timestamp: now},
		{StepIndex: 3, Role: "assistant", Content: "done", Timestamp: now},
	}

	points := turnsToPoints(cfg, summary, turns)
	if len(points) != len(turns) {
		t.Fatalf("got %d points for %d turns", len(points), len(turns))
	}

	seen := map[string]string{}
	for _, point := range points {
		if previous, clash := seen[point.ID]; clash {
			t.Errorf("point id %s reused by %q and %q", point.ID, previous, point.Payload.Content)
		}
		seen[point.ID] = point.Payload.Content
	}
}

// The first turn of a step keeps the identity it had before ordinals existed,
// so re-ingesting adds the previously lost turns without duplicating the rest.
func TestFirstTurnOfAStepKeepsItsIdentity(t *testing.T) {
	t.Parallel()
	withoutOrdinal := pointID("claude", "session-1", 7, 0)
	if withOrdinal := pointID("claude", "session-1", 7, 1); withOrdinal == withoutOrdinal {
		t.Fatal("a later turn in the same step reused the first turn's id")
	}
	if again := pointID("claude", "session-1", 7, 0); again != withoutOrdinal {
		t.Error("point ids are not stable across calls")
	}
}

// Skipped turns must not consume an ordinal, or the stored ids would shift
// whenever an empty turn appeared or disappeared next to a real one.
func TestSkippedTurnsDoNotConsumeAnOrdinal(t *testing.T) {
	t.Parallel()
	cfg := &Config{Hostname: "test-host"}
	summary := provider.ConversationSummary{Provider: "antigravity", ID: "session-1"}
	now := time.Now()

	withGap := turnsToPoints(cfg, summary, []provider.TurnInfo{
		{StepIndex: 4, Role: "assistant", Content: "", Timestamp: now},
		{StepIndex: 4, Role: "assistant", Content: "real", Timestamp: now},
	})
	without := turnsToPoints(cfg, summary, []provider.TurnInfo{
		{StepIndex: 4, Role: "assistant", Content: "real", Timestamp: now},
	})

	if len(withGap) != 1 || len(without) != 1 {
		t.Fatalf("expected one point each, got %d and %d", len(withGap), len(without))
	}
	if withGap[0].ID != without[0].ID {
		t.Error("an adjacent empty turn changed the stored id")
	}
}
