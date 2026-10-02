package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	_ "agentcli.local/ai/internal/provider/claude"
)

// fakeQdrant records what an ingestion pass sent, so tests never touch a real
// instance.
type fakeQdrant struct {
	mu            sync.Mutex
	server        *httptest.Server
	created       bool
	indexes       []string
	badQuery      []string
	points        map[string]Payload
	upsertCalls   int
	lastWait      string
	missingVector int
	scrollFilters []map[string]any
	// vectors makes a pre-existing collection report a vector config.
	vectors bool
	// exists makes the collection appear to already be present.
	exists            bool
	countFilters      []map[string]any
	deleteFilters     []map[string]any
	unfilteredDeletes int
}

// payloadField reads the payload field a filter clause names.
func payloadField(p Payload, key string) (string, bool) {
	switch key {
	case "session_id":
		return p.SessionID, true
	case "provider":
		return p.Provider, true
	case "hostname":
		return p.Hostname, true
	case "project_path":
		return p.ProjectPath, true
	case "role":
		return p.Role, true
	case "tool_name":
		return p.ToolName, true
	case "title":
		return p.Title, true
	case "content":
		return p.Content, true
	case "created_at":
		return p.CreatedAt, true
	}
	return "", false
}

// matchesClause evaluates one Qdrant filter clause: an exact keyword match, a
// full-text substring match, or a datetime lower bound.
func matchesClause(id string, p Payload, clause map[string]any) bool {
	if ids, ok := clause["has_id"].([]any); ok {
		for _, candidate := range ids {
			if text, ok := candidate.(string); ok && text == id {
				return true
			}
		}
		return false
	}

	key, _ := clause["key"].(string)
	value, known := payloadField(p, key)
	if !known {
		return false
	}

	if match, ok := clause["match"].(map[string]any); ok {
		if exact, ok := match["value"].(string); ok {
			return value == exact
		}
		if text, ok := match["text"].(string); ok {
			return strings.Contains(strings.ToLower(value), strings.ToLower(text))
		}
	}
	if bounds, ok := clause["range"].(map[string]any); ok {
		if gte, ok := bounds["gte"].(string); ok {
			return value >= gte
		}
	}
	return false
}

// matchesFilter applies the must and must_not clauses the query layer builds.
// The fake honours filters deliberately: a fake that returned every point
// regardless would make each query test pass without proving anything.
func matchesFilter(id string, p Payload, filter map[string]any) bool {
	if filter == nil {
		return true
	}
	if must, ok := filter["must"].([]any); ok {
		for _, raw := range must {
			clause, ok := raw.(map[string]any)
			if !ok || !matchesClause(id, p, clause) {
				return false
			}
		}
	}
	if mustNot, ok := filter["must_not"].([]any); ok {
		for _, raw := range mustNot {
			clause, ok := raw.(map[string]any)
			if ok && matchesClause(id, p, clause) {
				return false
			}
		}
	}
	return true
}

// writeScroll answers a scroll, honouring the filter, created_at ordering and
// the start_from cursor the ordered listing pages with.
func (f *fakeQdrant) writeScroll(w http.ResponseWriter, limit int, filter map[string]any, orderBy map[string]any) {
	type entry struct {
		id      string
		payload Payload
	}

	var kept []entry
	for id, payload := range f.points {
		if matchesFilter(id, payload, filter) {
			kept = append(kept, entry{id, payload})
		}
	}

	ordered := orderBy != nil
	if ordered {
		sort.Slice(kept, func(i, j int) bool {
			if kept[i].payload.CreatedAt != kept[j].payload.CreatedAt {
				return kept[i].payload.CreatedAt > kept[j].payload.CreatedAt
			}
			return kept[i].id < kept[j].id
		})
		if from, ok := orderBy["start_from"].(string); ok {
			var fromCursor []entry
			for _, e := range kept {
				if e.payload.CreatedAt <= from {
					fromCursor = append(fromCursor, e)
				}
			}
			kept = fromCursor
		}
	} else {
		sort.Slice(kept, func(i, j int) bool { return kept[i].id < kept[j].id })
	}

	if limit > 0 && len(kept) > limit {
		kept = kept[:limit]
	}

	encoded := make([]string, 0, len(kept))
	for _, e := range kept {
		payload, err := json.Marshal(e.payload)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		encoded = append(encoded, fmt.Sprintf(`{"id":%q,"payload":%s}`, e.id, payload))
	}
	_, _ = w.Write([]byte(`{"result":{"points":[` + strings.Join(encoded, ",") + `],"next_page_offset":null}}`))
}

func newFakeQdrant(t *testing.T) *fakeQdrant {
	t.Helper()
	f := &fakeQdrant{points: map[string]Payload{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/collections/", f.handle)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// handle dispatches the Qdrant endpoints the package uses.
func (f *fakeQdrant) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/collections/")

	// url.JoinPath escapes a "?" inside a path element, which silently turns
	// "index?wait=true" into a 404 path. Record any request whose query leaked
	// into the path so a test can fail on it.
	if strings.ContainsAny(r.URL.Path, "?%") {
		f.badQuery = append(f.badQuery, r.URL.Path)
	}

	switch {
	case r.Method == http.MethodGet && !strings.Contains(path, "/"):
		f.describeCollection(w)
	case r.Method == http.MethodPut && !strings.Contains(path, "/"):
		f.created = true
		_, _ = w.Write([]byte(`{"result":true}`))
	case r.Method == http.MethodPut && strings.HasPrefix(pathTail(path), "index"):
		f.addIndex(w, r)
	case r.Method == http.MethodPost && pathTail(path) == "points/scroll":
		f.scroll(w, r)
	case r.Method == http.MethodPost && pathTail(path) == "facet":
		f.facet(w)
	case r.Method == http.MethodPost && strings.HasPrefix(pathTail(path), "points/count"):
		f.count(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(pathTail(path), "points/delete"):
		f.deletePoints(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(pathTail(path), "points"):
		f.upsert(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeQdrant) describeCollection(w http.ResponseWriter) {
	if !f.exists && !f.created {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":{"error":"Not Found"}}`))
		return
	}
	vectors := "{}"
	if f.vectors {
		vectors = `{"size":1536,"distance":"Cosine"}`
	}
	_, _ = w.Write([]byte(`{"result":{"status":"green","points_count":` +
		strconv.Itoa(len(f.points)) + `,"config":{"params":{"vectors":` + vectors + `}}}}`))
}

func (f *fakeQdrant) addIndex(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FieldName string `json:"field_name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.indexes = append(f.indexes, body.FieldName)
	_, _ = w.Write([]byte(`{"result":true}`))
}

func (f *fakeQdrant) scroll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Filter  map[string]any `json:"filter"`
		Limit   int            `json:"limit"`
		OrderBy map[string]any `json:"order_by"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	f.scrollFilters = append(f.scrollFilters, req.Filter)
	f.writeScroll(w, req.Limit, req.Filter, req.OrderBy)
}

// count answers points/count, which is how verify and cleanup learn how many
// points a session has.
func (f *fakeQdrant) count(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Filter map[string]any `json:"filter"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	f.countFilters = append(f.countFilters, req.Filter)

	n := 0
	for id, payload := range f.points {
		if matchesFilter(id, payload, req.Filter) {
			n++
		}
	}
	_, _ = w.Write([]byte(`{"result":{"count":` + strconv.Itoa(n) + `}}`))
}

// deletePoints answers points/delete. A request with no filter is rejected the
// way Qdrant would treat it — as the whole collection — so a test can prove the
// client never sends one.
func (f *fakeQdrant) deletePoints(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Filter map[string]any `json:"filter"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Filter == nil {
		f.unfilteredDeletes++
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.deleteFilters = append(f.deleteFilters, req.Filter)

	for id, payload := range f.points {
		if matchesFilter(id, payload, req.Filter) {
			delete(f.points, id)
		}
	}
	_, _ = w.Write([]byte(`{"result":{"status":"acknowledged"}}`))
}

func (f *fakeQdrant) facet(w http.ResponseWriter) {
	sessions := map[string]struct{}{}
	for _, payload := range f.points {
		sessions[payload.SessionID] = struct{}{}
	}
	var hits []string
	for session := range sessions {
		hits = append(hits, fmt.Sprintf(`{"value":%q,"count":1}`, session))
	}
	sort.Strings(hits)
	_, _ = w.Write([]byte(`{"result":{"hits":[` + strings.Join(hits, ",") + `]}}`))
}

// upsert mirrors Qdrant's rejection of a point with no vector field, even when
// the collection declares none.
func (f *fakeQdrant) upsert(w http.ResponseWriter, r *http.Request) {
	var raw struct {
		Points []struct {
			ID      string          `json:"id"`
			Vector  json.RawMessage `json:"vector"`
			Payload Payload         `json:"payload"`
		} `json:"points"`
	}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	for _, p := range raw.Points {
		if len(p.Vector) == 0 {
			f.missingVector++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"status":{"error":"Format error in JSON body: missing field ` + "`vector`" + `"}}`))
			return
		}
	}

	f.upsertCalls++
	f.lastWait = r.URL.Query().Get("wait")
	for _, p := range raw.Points {
		f.points[p.ID] = p.Payload
	}
	_, _ = w.Write([]byte(`{"result":{"status":"completed"}}`))
}

func pathTail(path string) string {
	if idx := strings.Index(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return ""
}

// claudeFixture writes a Claude transcript under a temporary HOME.
func claudeFixture(t *testing.T, home, id string, messages ...string) {
	t.Helper()
	dir := filepath.Join(home, ".claude/projects/-tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var lines strings.Builder
	for i, message := range messages {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		line, err := json.Marshal(map[string]any{
			"type":      role,
			"cwd":       "/tmp/project",
			"timestamp": "2026-09-27T04:00:0" + strconv.Itoa(i%10) + "Z",
			"message":   map[string]any{"role": role, "content": message},
		})
		if err != nil {
			t.Fatal(err)
		}
		lines.Write(line)
		lines.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testConfig(t *testing.T, fake *fakeQdrant) *Config {
	t.Helper()
	t.Setenv(EnvQdrantURL, fake.server.URL)
	t.Setenv(EnvCollection, "test_collection")
	t.Setenv(EnvHostname, "test-host")
	t.Setenv(EnvOffsetsFile, filepath.Join(t.TempDir(), "offsets.json"))

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestConfigGate(t *testing.T) {
	t.Setenv(EnvQdrantURL, "")
	if Configured() {
		t.Error("Configured() is true with no endpoint set")
	}
	if _, err := LoadConfig(); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("LoadConfig error = %v, want ErrNotConfigured", err)
	}

	t.Setenv(EnvQdrantURL, "not-a-url")
	if _, err := LoadConfig(); err == nil {
		t.Error("a schemeless endpoint was accepted")
	}

	t.Setenv(EnvQdrantURL, "http://example.invalid:6333")
	t.Setenv(EnvCollection, "")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Collection != DefaultCollection {
		t.Errorf("default collection = %q, want %q", cfg.Collection, DefaultCollection)
	}
}

func TestIngestPublishesAndSkipsUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claudeFixture(t, home, "11111111-1111-4111-8111-111111111111", "first question", "first answer")

	fake := newFakeQdrant(t)
	cfg := testConfig(t, fake)

	result, err := Run(context.Background(), cfg, Options{Provider: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Published != 1 || result.Points != 2 {
		t.Fatalf("first pass published %d conversations / %d points, want 1/2 (%+v)", result.Published, result.Points, result)
	}
	if !fake.created {
		t.Error("collection was not created")
	}
	if len(fake.indexes) != len(payloadIndexes) {
		t.Errorf("created %d payload indexes, want %d", len(fake.indexes), len(payloadIndexes))
	}
	if fake.missingVector > 0 {
		t.Errorf("%d upserts omitted the vector field Qdrant requires", fake.missingVector)
	}
	if len(fake.badQuery) > 0 {
		t.Errorf("query parameters were escaped into the request path: %v", fake.badQuery)
	}
	if fake.lastWait != "true" {
		t.Errorf("wait query parameter = %q, want \"true\"", fake.lastWait)
	}

	// A second pass over unchanged data must not re-send anything.
	callsAfterFirst := fake.upsertCalls
	second, err := Run(context.Background(), cfg, Options{Provider: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Published != 0 || second.Skipped != 1 {
		t.Errorf("second pass published %d / skipped %d, want 0/1", second.Published, second.Skipped)
	}
	if fake.upsertCalls != callsAfterFirst {
		t.Errorf("unchanged conversation was re-sent (%d upserts, was %d)", fake.upsertCalls, callsAfterFirst)
	}

	// --force republishes regardless of the offset store.
	forced, err := Run(context.Background(), cfg, Options{Provider: "claude", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if forced.Published != 1 {
		t.Errorf("--force published %d, want 1", forced.Published)
	}
}

// Point IDs must be stable so that re-ingesting updates a turn in place. The
// retired daemon hashed the timestamp into the ID, so any reparse duplicated it.
func TestPointIDsAreStableAcrossRuns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claudeFixture(t, home, "22222222-2222-4222-8222-222222222222", "hello", "world")

	fake := newFakeQdrant(t)
	cfg := testConfig(t, fake)

	if _, err := Run(context.Background(), cfg, Options{Provider: "claude", Force: true}); err != nil {
		t.Fatal(err)
	}
	afterFirst := len(fake.points)

	if _, err := Run(context.Background(), cfg, Options{Provider: "claude", Force: true}); err != nil {
		t.Fatal(err)
	}
	if len(fake.points) != afterFirst {
		t.Errorf("re-ingesting created %d points, want a stable %d", len(fake.points), afterFirst)
	}

	for _, payload := range fake.points {
		if payload.Hostname != "test-host" {
			t.Errorf("hostname = %q, want the configured override", payload.Hostname)
		}
		if payload.ProjectPath != "/tmp/project" {
			t.Errorf("project_path = %q, want the conversation's workspace", payload.ProjectPath)
		}
		if payload.SessionID == "" || payload.Content == "" {
			t.Errorf("incomplete payload: %+v", payload)
		}
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claudeFixture(t, home, "33333333-3333-4333-8333-333333333333", "question", "answer")

	fake := newFakeQdrant(t)
	cfg := testConfig(t, fake)

	result, err := Run(context.Background(), cfg, Options{Provider: "claude", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Points != 2 {
		t.Errorf("dry run counted %d points, want 2", result.Points)
	}
	if fake.upsertCalls != 0 || fake.created {
		t.Error("dry run contacted the collection")
	}
	if _, err := os.Stat(cfg.OffsetsFile); err == nil {
		t.Error("dry run wrote the offset store")
	}
}

// Writing payload-only points into a collection that declares vectors fails at
// the server; refuse up front with an explanation instead.
func TestRefusesCollectionThatDeclaresVectors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claudeFixture(t, home, "44444444-4444-4444-8444-444444444444", "hi")

	fake := newFakeQdrant(t)
	fake.exists = true
	fake.vectors = true
	cfg := testConfig(t, fake)

	_, err := Run(context.Background(), cfg, Options{Provider: "claude"})
	if err == nil {
		t.Fatal("writing into a vector collection was allowed")
	}
	if !strings.Contains(err.Error(), "declares vectors") || !strings.Contains(err.Error(), EnvCollection) {
		t.Errorf("error does not explain the fix: %v", err)
	}
}

func TestStatusReportsTrackedAndRemoteState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claudeFixture(t, home, "55555555-5555-4555-8555-555555555555", "q", "a")

	fake := newFakeQdrant(t)
	cfg := testConfig(t, fake)
	if _, err := Run(context.Background(), cfg, Options{Provider: "claude"}); err != nil {
		t.Fatal(err)
	}

	status := GetStatus(context.Background(), cfg)
	if !status.Reachable || status.Error != "" {
		t.Fatalf("status not reachable: %+v", status)
	}
	if status.TrackedLocal != 1 || status.TrackedPoints != 2 {
		t.Errorf("tracked %d conversations / %d points, want 1/2", status.TrackedLocal, status.TrackedPoints)
	}
	if status.RemotePoints != 2 {
		t.Errorf("remote points = %d, want 2", status.RemotePoints)
	}
	if status.LastIngestAt == "" {
		t.Error("status did not record when the last ingest ran")
	}
}

func TestWatchRootsComeFromProviders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claudeFixture(t, home, "66666666-6666-4666-8666-666666666666", "hi")

	roots, err := WatchRoots("claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) == 0 {
		t.Fatal("no transcript roots reported for a provider with a transcript on disk")
	}
	want := filepath.Join(home, ".claude/projects")
	if roots[0] != want {
		t.Errorf("root = %q, want %q", roots[0], want)
	}

	// A provider must not report a directory that does not exist.
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			t.Errorf("reported a missing root %q", root)
		}
	}
}
