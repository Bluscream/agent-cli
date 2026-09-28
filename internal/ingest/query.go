package ingest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
)

// QueryOptions narrows a remote query. Zero values mean "no filter".
type QueryOptions struct {
	Provider  string
	Workspace string
	Role      string
	Text      string
	SessionID string
	Since     time.Time
	Limit     int
}

// RemoteMatch is one matching turn from the collection.
type RemoteMatch struct {
	SessionID   string
	Provider    string
	Title       string
	ProjectPath string
	Role        string
	Content     string
	StepIndex   int
	CreatedAt   time.Time
}

const (
	scrollPageSize = 512
	// maxOrderedPages bounds a listing so an enormous collection cannot turn
	// one command into an unbounded crawl.
	maxOrderedPages = 40
)

// buildFilter turns the options into a Qdrant filter, or nil when unfiltered.
func buildFilter(opts QueryOptions) any {
	var must []any

	add := func(key, value string) {
		if value != "" {
			must = append(must, map[string]any{"key": key, "match": map[string]any{"value": value}})
		}
	}
	add("provider", opts.Provider)
	add("session_id", opts.SessionID)
	add("role", opts.Role)
	add("project_path", opts.Workspace)

	if opts.Text != "" {
		must = append(must, map[string]any{"key": "content", "match": map[string]any{"text": opts.Text}})
	}
	if !opts.Since.IsZero() {
		must = append(must, map[string]any{
			"key":         "created_at",
			"range":       map[string]any{"gte": opts.Since.UTC().Format(time.RFC3339)},
			"is_datetime": true,
		})
	}

	if len(must) == 0 {
		return nil
	}
	return map[string]any{"must": must}
}

func parseStamp(raw string) time.Time {
	stamp, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return stamp
}

// ensureReachable turns a connection or missing-collection problem into an
// error that names the fix, since these are the common first-run failures.
func ensureReachable(ctx context.Context, cfg *Config, client *qdrantClient) error {
	info, err := client.GetCollection(ctx, cfg.Collection)
	if err != nil {
		return fmt.Errorf("reaching %s at %s: %w", cfg.Collection, cfg.QdrantURL.Redacted(), err)
	}
	if info == nil {
		return fmt.Errorf(
			"collection %q does not exist at %s; run `ai ingest` to create and fill it",
			cfg.Collection, cfg.QdrantURL.Redacted())
	}
	return nil
}

// RemoteSearch returns turns matching the options, newest first.
func RemoteSearch(ctx context.Context, cfg *Config, opts QueryOptions) ([]RemoteMatch, error) {
	client := newQdrantClient(cfg)
	if err := ensureReachable(ctx, cfg, client); err != nil {
		return nil, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	page, err := client.Scroll(ctx, cfg.Collection, scrollRequest{
		Filter:      buildFilter(opts),
		Limit:       limit,
		WithPayload: true,
		OrderBy:     map[string]any{"key": "created_at", "direction": "desc"},
	})
	if err != nil {
		return nil, err
	}

	matches := make([]RemoteMatch, 0, len(page.Points))
	for _, point := range page.Points {
		matches = append(matches, RemoteMatch{
			SessionID:   point.Payload.SessionID,
			Provider:    point.Payload.Provider,
			Title:       point.Payload.Title,
			ProjectPath: point.Payload.ProjectPath,
			Role:        point.Payload.Role,
			Content:     point.Payload.Content,
			StepIndex:   point.Payload.StepIndex,
			CreatedAt:   parseStamp(point.Payload.CreatedAt),
		})
	}
	return matches, nil
}

// RemoteConversations lists the most recently active conversations by walking
// points newest-first and collapsing them by session.
func RemoteConversations(ctx context.Context, cfg *Config, opts QueryOptions) ([]provider.ConversationSummary, error) {
	client := newQdrantClient(cfg)
	if err := ensureReachable(ctx, cfg, client); err != nil {
		return nil, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 25
	}
	filter := buildFilter(opts)

	byID := map[string]*provider.ConversationSummary{}
	var order []string
	var cursor string
	var seenAtCursor []string

	for page := 0; page < maxOrderedPages && len(order) < limit; page++ {
		orderBy := map[string]any{"key": "created_at", "direction": "desc"}
		pageFilter := filter
		if cursor != "" {
			orderBy["start_from"] = cursor
			pageFilter = excludeIDs(filter, seenAtCursor)
		}

		result, err := client.Scroll(ctx, cfg.Collection, scrollRequest{
			Filter:      pageFilter,
			Limit:       scrollPageSize,
			WithPayload: []string{"session_id", "provider", "title", "project_path", "created_at"},
			OrderBy:     orderBy,
		})
		if err != nil {
			return nil, err
		}
		if len(result.Points) == 0 {
			break
		}

		for _, point := range result.Points {
			collect(byID, &order, point.Payload)
		}

		last := result.Points[len(result.Points)-1].Payload.CreatedAt
		if last == cursor {
			// Every point on this page shares the boundary timestamp; advancing
			// further would repeat it forever.
			break
		}
		cursor = last
		seenAtCursor = idsAt(result.Points, cursor)
	}

	summaries := make([]provider.ConversationSummary, 0, min(len(order), limit))
	for _, id := range order {
		if len(summaries) == limit {
			break
		}
		summaries = append(summaries, *byID[id])
	}
	return summaries, nil
}

// collect folds one point into the per-conversation summary being built.
func collect(byID map[string]*provider.ConversationSummary, order *[]string, payload Payload) {
	stamp := parseStamp(payload.CreatedAt)

	existing, ok := byID[payload.SessionID]
	if !ok {
		byID[payload.SessionID] = &provider.ConversationSummary{
			Provider:      payload.Provider,
			ID:            payload.SessionID,
			Title:         payload.Title,
			WorkspaceDir:  payload.ProjectPath,
			MessagesCount: 1,
			// A remote record has no transcript file, so its size is unknown
			// rather than zero.
			TotalSizeBytes: -1,
			CreatedAt:      stamp,
			UpdatedAt:      stamp,
		}
		*order = append(*order, payload.SessionID)
		return
	}

	existing.MessagesCount++
	if !stamp.IsZero() {
		if existing.CreatedAt.IsZero() || stamp.Before(existing.CreatedAt) {
			existing.CreatedAt = stamp
		}
		if stamp.After(existing.UpdatedAt) {
			existing.UpdatedAt = stamp
		}
	}
}

func idsAt(points []scrollPoint, stamp string) []string {
	var ids []string
	for _, point := range points {
		if point.Payload.CreatedAt == stamp {
			ids = append(ids, point.ID)
		}
	}
	return ids
}

// excludeIDs adds a has_id exclusion to a filter without mutating the original.
func excludeIDs(filter any, ids []string) any {
	if len(ids) == 0 {
		return filter
	}
	combined := map[string]any{"must_not": []any{map[string]any{"has_id": ids}}}
	if existing, ok := filter.(map[string]any); ok {
		for key, value := range existing {
			combined[key] = value
		}
	}
	return combined
}

// maxFacetSessions bounds session enumeration when resolving a short ID.
const maxFacetSessions = 10000

// resolveSessionID expands a short ID or prefix to the stored session ID, so
// remote lookups accept the same identifiers as local ones. It returns the
// query unchanged when it already matches a stored session exactly.
func resolveSessionID(ctx context.Context, cfg *Config, client *qdrantClient, query string) (string, error) {
	sessions, err := client.FacetValues(ctx, cfg.Collection, "session_id", maxFacetSessions)
	if err != nil {
		// Without the facet endpoint the exact ID is still worth trying.
		return query, nil
	}

	var matches []string
	for _, session := range sessions {
		if session == query {
			return session, nil
		}
		if idutil.Match(query, session) {
			matches = append(matches, session)
		}
	}

	switch len(matches) {
	case 0:
		return query, nil
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%q matches %d ingested conversations: %s",
			query, len(matches), strings.Join(matches, ", "))
	}
}

// RemoteConversation rebuilds one conversation's turns from the collection.
func RemoteConversation(ctx context.Context, cfg *Config, sessionID string) (*provider.ConversationDetail, error) {
	client := newQdrantClient(cfg)
	if err := ensureReachable(ctx, cfg, client); err != nil {
		return nil, err
	}

	sessionID, err := resolveSessionID(ctx, cfg, client, sessionID)
	if err != nil {
		return nil, err
	}

	var payloads []Payload
	var offset any
	for {
		result, scrollErr := client.Scroll(ctx, cfg.Collection, scrollRequest{
			Filter:      buildFilter(QueryOptions{SessionID: sessionID}),
			Limit:       scrollPageSize,
			Offset:      offset,
			WithPayload: true,
		})
		if scrollErr != nil {
			return nil, scrollErr
		}
		for _, point := range result.Points {
			payloads = append(payloads, point.Payload)
		}
		if result.NextPageOffset == nil || len(result.Points) == 0 {
			break
		}
		offset = result.NextPageOffset
	}

	if len(payloads) == 0 {
		return nil, fmt.Errorf("no ingested conversation matches %q in %s", sessionID, cfg.Collection)
	}

	// Points come back in id order, which is a hash; step order is the only
	// meaningful reading order for a transcript.
	sort.Slice(payloads, func(i, j int) bool { return payloads[i].StepIndex < payloads[j].StepIndex })

	detail := &provider.ConversationDetail{
		Summary: provider.ConversationSummary{
			Provider:       payloads[0].Provider,
			ID:             payloads[0].SessionID,
			Title:          payloads[0].Title,
			WorkspaceDir:   payloads[0].ProjectPath,
			MessagesCount:  len(payloads),
			TotalSizeBytes: -1,
		},
	}

	for _, payload := range payloads {
		stamp := parseStamp(payload.CreatedAt)
		if !stamp.IsZero() {
			if detail.Summary.CreatedAt.IsZero() || stamp.Before(detail.Summary.CreatedAt) {
				detail.Summary.CreatedAt = stamp
			}
			if stamp.After(detail.Summary.UpdatedAt) {
				detail.Summary.UpdatedAt = stamp
			}
		}
		detail.Turns = append(detail.Turns, provider.TurnInfo{
			StepIndex: payload.StepIndex,
			Role:      payload.Role,
			Content:   payload.Content,
			Thinking:  payload.Thinking,
			ToolCall:  payload.ToolName,
			Timestamp: stamp,
		})
	}
	detail.InitialPrompt, detail.LastResponse = provider.Endpoints(detail.Turns)
	return detail, nil
}
