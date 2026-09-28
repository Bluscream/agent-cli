package ingest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fixtureAID = "11111111-1111-4111-8111-111111111111"
	fixtureBID = "22222222-2222-4222-8222-222222222222"
)

// ingestFixture publishes a Claude fixture conversation so verify has something
// to compare against. Everything is under t.TempDir and a fake Qdrant.
func ingestFixture(t *testing.T, home string, fake *fakeQdrant, cfg *Config, id string, messages ...string) {
	t.Helper()
	claudeFixture(t, home, id, messages...)
	if _, err := Run(context.Background(), cfg, Options{Provider: "claude"}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
}

func TestVerifyReportsAHealthyCollection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fake := newFakeQdrant(t)
	cfg := testConfig(t, fake)
	ingestFixture(t, home, fake, cfg, fixtureAID, "question", "answer")

	report, err := Verify(context.Background(), cfg, VerifyOptions{Provider: "claude"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !report.Healthy() {
		t.Fatalf("a freshly ingested collection did not verify: %+v", report.Issues)
	}
	if report.OK != 1 {
		t.Fatalf("verified %d conversations, want 1 (%+v)", report.OK, report)
	}
	if report.ExpectedPoints != int(report.StoredPoints) {
		t.Fatalf("expected %d points but %d are stored", report.ExpectedPoints, report.StoredPoints)
	}
}

// The defect a collection total cannot catch: one conversation absent.
func TestVerifyDetectsAConversationThatWasNeverPublished(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fake := newFakeQdrant(t)
	cfg := testConfig(t, fake)
	ingestFixture(t, home, fake, cfg, fixtureAID, "question", "answer")

	// A second conversation appears after the pass and is never published.
	claudeFixture(t, home, fixtureBID, "unpublished question", "unpublished answer")

	report, err := Verify(context.Background(), cfg, VerifyOptions{Provider: "claude"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Missing != 1 {
		t.Fatalf("missing = %d, want 1 (%+v)", report.Missing, report.Issues)
	}
	if report.Healthy() {
		t.Fatal("a collection missing a conversation reported healthy")
	}
	if len(report.Issues) != 1 || report.Issues[0].ID != fixtureBID {
		t.Fatalf("issues = %+v", report.Issues)
	}
}

// Counts can agree while the stored text does not. Only --deep sees it.
func TestVerifyDeepDetectsAlteredStoredContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fake := newFakeQdrant(t)
	cfg := testConfig(t, fake)
	ingestFixture(t, home, fake, cfg, fixtureAID, "question", "answer")

	fake.mu.Lock()
	for id, payload := range fake.points {
		payload.Content = "something else entirely"
		fake.points[id] = payload
		break
	}
	fake.mu.Unlock()

	shallow, err := Verify(context.Background(), cfg, VerifyOptions{Provider: "claude"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !shallow.Healthy() {
		t.Fatal("the count-only pass should not have noticed altered text")
	}

	deep, err := Verify(context.Background(), cfg, VerifyOptions{Provider: "claude", Deep: true})
	if err != nil {
		t.Fatalf("deep verify: %v", err)
	}
	if deep.Healthy() {
		t.Fatal("the deep pass did not notice altered text")
	}
	if deep.Issues[0].MissingContent != 1 {
		t.Fatalf("missing content = %d, want 1 (%+v)", deep.Issues[0].MissingContent, deep.Issues[0])
	}
}

func TestVerifyReportsOrphanedSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fake := newFakeQdrant(t)
	cfg := testConfig(t, fake)
	ingestFixture(t, home, fake, cfg, fixtureAID, "question", "answer")

	// The transcript goes but the points stay: the state after a conversation
	// is deleted locally without --remote.
	removeFixture(t, home, fixtureAID)

	report, err := Verify(context.Background(), cfg, VerifyOptions{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(report.Orphans) != 1 || report.Orphans[0] != fixtureAID {
		t.Fatalf("orphans = %v, want [%s]", report.Orphans, fixtureAID)
	}
	if report.Healthy() {
		t.Fatal("a collection with an orphaned session reported healthy")
	}
}

// Narrowing the local side would report every conversation outside the filter
// as orphaned, so a narrowed pass must not look for orphans at all.
func TestVerifyDoesNotLookForOrphansWhenNarrowed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fake := newFakeQdrant(t)
	cfg := testConfig(t, fake)
	ingestFixture(t, home, fake, cfg, fixtureAID, "question", "answer")
	removeFixture(t, home, fixtureAID)

	report, err := Verify(context.Background(), cfg, VerifyOptions{Provider: "claude"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(report.Orphans) != 0 {
		t.Fatalf("a narrowed pass reported orphans: %v", report.Orphans)
	}
	if report.RemoteSessions != -1 {
		t.Fatalf("remote sessions = %d, want -1 (not enumerated)", report.RemoteSessions)
	}
}

func TestCleanupRemovesOrphanedSessionsOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fake := newFakeQdrant(t)
	cfg := testConfig(t, fake)
	ingestFixture(t, home, fake, cfg, fixtureAID, "question", "answer")
	ingestFixture(t, home, fake, cfg, fixtureBID, "kept question", "kept answer")
	removeFixture(t, home, fixtureAID)

	before := len(fake.points)
	result, err := Cleanup(context.Background(), cfg, CleanupOptions{})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if len(result.Sessions) != 1 || result.Sessions[0] != fixtureAID {
		t.Fatalf("cleaned %v, want [%s]", result.Sessions, fixtureAID)
	}
	if result.Points != 2 {
		t.Fatalf("removed %d points, want 2", result.Points)
	}
	if len(fake.points) != before-2 {
		t.Fatalf("collection holds %d points, want %d", len(fake.points), before-2)
	}
	if fake.unfilteredDeletes > 0 {
		t.Fatal("a delete was sent with no filter, which Qdrant reads as the whole collection")
	}

	// The conversation that still exists must be untouched and still verify.
	report, err := Verify(context.Background(), cfg, VerifyOptions{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !report.Healthy() {
		t.Fatalf("cleanup left the collection unhealthy: %+v", report)
	}
}

func TestCleanupDryRunRemovesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fake := newFakeQdrant(t)
	cfg := testConfig(t, fake)
	ingestFixture(t, home, fake, cfg, fixtureAID, "question", "answer")
	removeFixture(t, home, fixtureAID)

	before := len(fake.points)
	result, err := Cleanup(context.Background(), cfg, CleanupOptions{DryRun: true})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if result.Points != 2 {
		t.Fatalf("dry run counted %d points, want 2", result.Points)
	}
	if len(fake.points) != before {
		t.Fatalf("dry run deleted points: %d remain of %d", len(fake.points), before)
	}
	if len(fake.deleteFilters) != 0 {
		t.Fatal("dry run issued a delete")
	}
}

// A cleaned-up conversation must not stay recorded as up to date, or a later
// pass would skip it and never restore the points.
func TestCleanupForgetsTheOffsetRecord(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fake := newFakeQdrant(t)
	cfg := testConfig(t, fake)
	ingestFixture(t, home, fake, cfg, fixtureAID, "question", "answer")

	if _, err := Cleanup(context.Background(), cfg, CleanupOptions{Sessions: []string{fixtureAID}}); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	store := loadOffsets(cfg.OffsetsFile)
	if _, present := store.Records[offsetKey("claude", fixtureAID)]; present {
		t.Fatal("the offset record survived cleanup, so a later pass would skip the conversation")
	}

	// And a fresh pass must republish it rather than skipping.
	result, err := Run(context.Background(), cfg, Options{Provider: "claude"})
	if err != nil {
		t.Fatalf("re-ingest: %v", err)
	}
	if result.Published != 1 {
		t.Fatalf("re-ingest published %d, want 1 (%+v)", result.Published, result)
	}
}

// A provider that cannot be listed looks exactly like a provider with no
// conversations, and the second reading would delete every one of its sessions.
func TestOrphansRefusesWhenAProviderCannotBeListed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := newFakeQdrant(t)
	cfg := testConfig(t, fake)
	fake.exists = true

	if _, err := Orphans(context.Background(), cfg, "no-such-provider"); err == nil {
		t.Fatal("expected an error for an unlistable provider")
	} else if !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("error does not say it is refusing: %v", err)
	}
}

// removeFixture deletes a fixture transcript, simulating a conversation that no
// longer exists locally.
func removeFixture(t *testing.T, home, id string) {
	t.Helper()
	path := filepath.Join(home, ".claude/projects/-tmp", id+".jsonl")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove fixture: %v", err)
	}
}
