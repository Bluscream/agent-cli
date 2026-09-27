package ingest

import (
	"context"
	"strings"
	"testing"
	"time"

	"agentcli.local/ai/internal/idutil"
)

func TestBuildFilter(t *testing.T) {
	t.Parallel()

	if got := buildFilter(QueryOptions{}); got != nil {
		t.Errorf("empty options produced a filter: %#v", got)
	}

	filter, ok := buildFilter(QueryOptions{
		Provider:  "claude",
		Role:      "user",
		Text:      "deploy",
		SessionID: "abc",
		Workspace: "/tmp/project",
		Since:     time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}).(map[string]any)
	if !ok {
		t.Fatal("filter is not an object")
	}
	must, ok := filter["must"].([]any)
	if !ok {
		t.Fatal("filter has no must clause")
	}
	if len(must) != 6 {
		t.Errorf("must has %d clauses, want 6: %#v", len(must), must)
	}

	// The text clause has to use a full-text match; an exact value match would
	// only ever hit a turn whose whole content equals the query.
	var sawFullText, sawDatetime bool
	for _, clause := range must {
		entry, ok := clause.(map[string]any)
		if !ok {
			continue
		}
		if match, ok := entry["match"].(map[string]any); ok {
			if _, isText := match["text"]; isText {
				sawFullText = true
			}
		}
		if entry["is_datetime"] == true {
			sawDatetime = true
		}
	}
	if !sawFullText {
		t.Error("text query was not expressed as a full-text match")
	}
	if !sawDatetime {
		t.Error("since filter was not marked as a datetime range")
	}
}

func TestExcludeIDsKeepsOriginalFilter(t *testing.T) {
	t.Parallel()
	original := map[string]any{"must": []any{"clause"}}

	combined, ok := excludeIDs(original, []string{"id-1"}).(map[string]any)
	if !ok {
		t.Fatal("combined filter is not an object")
	}
	if _, present := combined["must"]; !present {
		t.Error("the original must clause was dropped")
	}
	if _, present := combined["must_not"]; !present {
		t.Error("the id exclusion was not added")
	}
	if _, mutated := original["must_not"]; mutated {
		t.Error("the caller's filter was mutated")
	}
	if got := excludeIDs(original, nil); got == nil {
		t.Error("an empty id list dropped the filter entirely")
	}
}

// A missing collection is the normal first-run state and must say what to do.
func TestRemoteQueryErrorsNameTheFix(t *testing.T) {
	fake := newFakeQdrant(t)
	cfg := testConfig(t, fake)

	_, err := RemoteSearch(context.Background(), cfg, QueryOptions{Text: "anything"})
	if err == nil {
		t.Fatal("querying an absent collection succeeded")
	}
	if !strings.Contains(err.Error(), "ai ingest") {
		t.Errorf("error does not point at the command that fixes it: %v", err)
	}
	if !strings.Contains(err.Error(), cfg.Collection) {
		t.Errorf("error does not name the collection: %v", err)
	}
}

// seedRemote publishes a fixture conversation so the remote readers have data.
func seedRemote(t *testing.T, id string, messages ...string) (*Config, *fakeQdrant) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	claudeFixture(t, home, id, messages...)

	fake := newFakeQdrant(t)
	cfg := testConfig(t, fake)
	if _, err := Run(context.Background(), cfg, Options{Provider: "claude"}); err != nil {
		t.Fatal(err)
	}
	return cfg, fake
}

func TestRemoteSearchReturnsStoredTurns(t *testing.T) {
	const id = "77777777-7777-4777-8777-777777777777"
	cfg, _ := seedRemote(t, id, "find the needle here", "an answer")

	matches, err := RemoteSearch(context.Background(), cfg, QueryOptions{Text: "needle", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no matches returned from the collection")
	}
	for _, m := range matches {
		if m.SessionID != id {
			t.Errorf("session_id = %q, want %q", m.SessionID, id)
		}
		if m.ProjectPath != "/tmp/project" {
			t.Errorf("project_path = %q, want the conversation workspace", m.ProjectPath)
		}
		if m.CreatedAt.IsZero() {
			t.Error("created_at did not parse")
		}
	}
}

func TestRemoteConversationsCollapseBySession(t *testing.T) {
	const id = "88888888-8888-4888-8888-888888888888"
	cfg, _ := seedRemote(t, id, "first", "second", "third", "fourth")

	summaries, err := RemoteConversations(context.Background(), cfg, QueryOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("got %d conversations, want the 4 turns collapsed into 1", len(summaries))
	}

	got := summaries[0]
	if got.ID != id {
		t.Errorf("id = %q, want %q", got.ID, id)
	}
	if got.MessagesCount != 4 {
		t.Errorf("messages = %d, want 4", got.MessagesCount)
	}
	// A remote record has no file behind it, so its size must read as unknown
	// rather than as an empty transcript.
	if got.TotalSizeBytes >= 0 {
		t.Errorf("size = %d, want a negative unknown marker", got.TotalSizeBytes)
	}
	if got.CreatedAt.After(got.UpdatedAt) {
		t.Errorf("created %s is after updated %s", got.CreatedAt, got.UpdatedAt)
	}
}

// Points come back in id order, which is a hash, so turns must be sorted by
// step index or the transcript reads in a random order.
func TestRemoteConversationOrdersByStepIndex(t *testing.T) {
	const id = "99999999-9999-4999-8999-999999999999"
	cfg, _ := seedRemote(t, id, "one", "two", "three", "four", "five", "six")

	detail, err := RemoteConversation(context.Background(), cfg, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Turns) != 6 {
		t.Fatalf("got %d turns, want 6", len(detail.Turns))
	}
	for i := 1; i < len(detail.Turns); i++ {
		if detail.Turns[i-1].StepIndex > detail.Turns[i].StepIndex {
			t.Fatalf("turns out of order at %d: %d then %d",
				i, detail.Turns[i-1].StepIndex, detail.Turns[i].StepIndex)
		}
	}
	if detail.Summary.ID != id || detail.Summary.Title == "" {
		t.Errorf("summary incomplete: %+v", detail.Summary)
	}
	if detail.InitialPrompt == "" {
		t.Error("initial prompt not recovered")
	}
}

// Remote lookups must accept the short IDs that every other command accepts.
func TestRemoteConversationResolvesShortID(t *testing.T) {
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	cfg, _ := seedRemote(t, id, "question", "answer")

	short := shortIDOf(id)
	detail, err := RemoteConversation(context.Background(), cfg, short)
	if err != nil {
		t.Fatalf("short ID %q did not resolve: %v", short, err)
	}
	if detail.Summary.ID != id {
		t.Errorf("resolved to %q, want %q", detail.Summary.ID, id)
	}

	if _, err := RemoteConversation(context.Background(), cfg, "not-a-known-id"); err == nil {
		t.Error("an unknown id was accepted")
	}
}

// shortIDOf mirrors the short ID the CLI displays for a conversation.
func shortIDOf(id string) string {
	return idutil.ShortID(id)
}
