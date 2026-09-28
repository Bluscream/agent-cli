package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// qdrantClient speaks the handful of Qdrant REST endpoints this package needs.
// The official Go client is gRPC-first and pulls grpc, protobuf and genproto
// into a module that otherwise has nine dependencies, which is a poor trade for
// five JSON calls against a documented, stable REST API.
type qdrantClient struct {
	base   *url.URL
	apiKey string
	http   *http.Client
}

func newQdrantClient(cfg *Config) *qdrantClient {
	return &qdrantClient{
		base:   cfg.QdrantURL,
		apiKey: cfg.APIKey,
		// A default http.Client has no timeout at all; a stalled NAS would
		// otherwise hang ingestion indefinitely.
		http: &http.Client{Timeout: 60 * time.Second},
	}
}

// CollectionInfo is the subset of Qdrant's collection metadata this package reads.
type CollectionInfo struct {
	Status              string `json:"status"`
	PointsCount         int64  `json:"points_count"`
	IndexedVectorsCount int64  `json:"indexed_vectors_count"`
	Config              struct {
		Params struct {
			// Vectors is absent or empty for a payload-only collection and an
			// object describing dimensions for a vector one.
			Vectors json.RawMessage `json:"vectors"`
		} `json:"params"`
	} `json:"config"`
}

// HasVectors reports whether the collection declares a vector configuration,
// which means every point written to it must carry a vector.
func (c *CollectionInfo) HasVectors() bool {
	trimmed := strings.TrimSpace(string(c.Config.Params.Vectors))
	return trimmed != "" && trimmed != "null" && trimmed != "{}"
}

// do issues one request. Query parameters are passed separately because
// url.JoinPath percent-escapes a "?" inside a path element, which turns
// "index?wait=true" into a path segment Qdrant answers with 404.
func (q *qdrantClient) do(ctx context.Context, method string, path []string, query url.Values, body, out any) error {
	endpoint := q.base.JoinPath(path...)
	if len(query) > 0 {
		endpoint.RawQuery = query.Encode()
	}

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request for %s: %w", endpoint.Path, err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return fmt.Errorf("building request for %s: %w", endpoint.Path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if q.apiKey != "" {
		req.Header.Set("api-key", q.apiKey)
	}

	resp, err := q.http.Do(req)
	if err != nil {
		return fmt.Errorf("calling %s: %w", endpoint.Redacted(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("%s %s: %s: %s", method, endpoint.Path, resp.Status, strings.TrimSpace(string(detail)))
	}
	if out == nil {
		return nil
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decoding response from %s: %w", endpoint.Path, err)
	}
	if len(envelope.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("decoding result from %s: %w", endpoint.Path, err)
	}
	return nil
}

// GetCollection returns the collection's metadata, or nil when it does not exist.
func (q *qdrantClient) GetCollection(ctx context.Context, name string) (*CollectionInfo, error) {
	var info CollectionInfo
	err := q.do(ctx, http.MethodGet, []string{"collections", name}, nil, nil, &info)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "Not Found") {
			return nil, nil
		}
		return nil, err
	}
	return &info, nil
}

// payloadIndexes mirror the fields the search tooling filters on. Without them
// Qdrant falls back to a full scan for every filtered query.
var payloadIndexes = []struct {
	Field  string
	Schema string
}{
	{"session_id", "keyword"},
	{"provider", "keyword"},
	{"hostname", "keyword"},
	{"project_path", "keyword"},
	{"role", "keyword"},
	{"tool_name", "keyword"},
	{"title", "text"},
	{"created_at", "datetime"},
	{"content", "text"},
}

// EnsureCollection creates the payload-only collection and its payload indexes
// when they are missing, and refuses to write into a collection that declares
// vectors, because every point here is vectorless.
func (q *qdrantClient) EnsureCollection(ctx context.Context, name string) error {
	info, err := q.GetCollection(ctx, name)
	if err != nil {
		return err
	}
	if info != nil {
		if info.HasVectors() {
			return fmt.Errorf(
				"collection %q declares vectors, but this writes payload-only points; "+
					"set %s to a different name (default %q) or delete that collection",
				name, EnvCollection, DefaultCollection)
		}
		return q.ensureIndexes(ctx, name)
	}

	create := map[string]any{"vectors": map[string]any{}, "on_disk_payload": true}
	if err := q.do(ctx, http.MethodPut, []string{"collections", name}, nil, create, nil); err != nil {
		return fmt.Errorf("creating collection %q: %w", name, err)
	}

	return q.ensureIndexes(ctx, name)
}

// ensureIndexes creates every payload index. Qdrant treats creating an index
// that already exists as success, so this also repairs a collection that was
// created by an earlier run that failed partway through.
func (q *qdrantClient) ensureIndexes(ctx context.Context, name string) error {
	wait := url.Values{"wait": []string{"true"}}
	for _, index := range payloadIndexes {
		body := map[string]any{"field_name": index.Field, "field_schema": index.Schema}
		if err := q.do(ctx, http.MethodPut, []string{"collections", name, "index"}, wait, body, nil); err != nil {
			return fmt.Errorf("indexing %q on %q: %w", index.Field, name, err)
		}
	}
	return nil
}

// scrollRequest is Qdrant's points/scroll body. Qdrant rejects an offset
// combined with order_by, so ordered paging uses order_by.start_from plus a
// filter excluding the ids already seen at that boundary value.
type scrollRequest struct {
	Filter      any `json:"filter,omitempty"`
	Limit       int `json:"limit"`
	Offset      any `json:"offset,omitempty"`
	WithPayload any `json:"with_payload"`
	OrderBy     any `json:"order_by,omitempty"`
}

type scrollPoint struct {
	ID      string  `json:"id"`
	Payload Payload `json:"payload"`
}

type scrollResponse struct {
	Points         []scrollPoint `json:"points"`
	NextPageOffset any           `json:"next_page_offset"`
}

// Scroll reads a page of points.
func (q *qdrantClient) Scroll(ctx context.Context, name string, req scrollRequest) (*scrollResponse, error) {
	if req.WithPayload == nil {
		req.WithPayload = true
	}
	var out scrollResponse
	if err := q.do(ctx, http.MethodPost, []string{"collections", name, "points", "scroll"}, nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FacetValues returns the distinct values of a keyword payload field. It is
// the cheap way to enumerate sessions: scrolling every point to collect them
// would read the whole collection.
func (q *qdrantClient) FacetValues(ctx context.Context, name, key string, limit int) ([]string, error) {
	body := map[string]any{"key": key, "limit": limit, "exact": true}

	var out struct {
		Hits []struct {
			Value string `json:"value"`
		} `json:"hits"`
	}
	if err := q.do(ctx, http.MethodPost, []string{"collections", name, "facet"}, nil, body, &out); err != nil {
		return nil, err
	}

	values := make([]string, 0, len(out.Hits))
	for _, hit := range out.Hits {
		values = append(values, hit.Value)
	}
	return values, nil
}

// Upsert writes points in batches. Qdrant accepts large payloads, but a batch
// that is too big turns one slow request into one failed request.
func (q *qdrantClient) Upsert(ctx context.Context, name string, points []Point) error {
	const batchSize = 256

	for start := 0; start < len(points); start += batchSize {
		end := min(start+batchSize, len(points))
		body := map[string]any{"points": points[start:end]}
		wait := url.Values{"wait": []string{"true"}}
		if err := q.do(ctx, http.MethodPut, []string{"collections", name, "points"}, wait, body, nil); err != nil {
			return fmt.Errorf("upserting points %d-%d into %q: %w", start, end, name, err)
		}
	}
	return nil
}

// Count returns how many points match the filter, exactly rather than
// estimated: an approximate count cannot answer "is this conversation stored".
// A nil filter counts the whole collection.
func (q *qdrantClient) Count(ctx context.Context, name string, filter any) (int64, error) {
	body := map[string]any{"exact": true}
	if filter != nil {
		body["filter"] = filter
	}
	var out struct {
		Count int64 `json:"count"`
	}
	if err := q.do(ctx, http.MethodPost, []string{"collections", name, "points", "count"}, nil, body, &out); err != nil {
		return 0, err
	}
	return out.Count, nil
}

// DeleteByFilter removes every point matching the filter. wait=true so the
// caller's subsequent count reflects the deletion rather than racing it.
//
// A nil filter is rejected: Qdrant would read that as "delete everything",
// and this is the one operation where an accidentally empty filter is
// unrecoverable.
func (q *qdrantClient) DeleteByFilter(ctx context.Context, name string, filter any) error {
	if filter == nil {
		return fmt.Errorf("refusing to delete from %q with no filter", name)
	}
	wait := url.Values{"wait": []string{"true"}}
	body := map[string]any{"filter": filter}
	return q.do(ctx, http.MethodPost, []string{"collections", name, "points", "delete"}, wait, body, nil)
}

// DeleteCollection removes the collection entirely. Absent is success: the
// caller asked for it to be gone.
func (q *qdrantClient) DeleteCollection(ctx context.Context, name string) error {
	err := q.do(ctx, http.MethodDelete, []string{"collections", name}, nil, nil, nil)
	if err != nil && (strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "Not Found")) {
		return nil
	}
	return err
}
