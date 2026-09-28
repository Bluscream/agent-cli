package ingest

import (
	"context"
	"fmt"
	"sort"
	"time"

	"agentcli.local/ai/internal/provider"
)

// Options controls one ingestion pass.
type Options struct {
	Provider string // restrict to one provider; empty means all
	Force    bool   // republish conversations even when unchanged
	DryRun   bool   // report what would be published without writing
	Since    time.Time
	// OnProgress, when set, is called once per conversation as the pass runs.
	OnProgress func(ProgressEvent)
}

// ProgressEvent reports one conversation's outcome and how far the pass has got.
type ProgressEvent struct {
	Done    int
	Total   int
	Points  int // points published so far across the pass
	Elapsed time.Duration
	Result  ConversationResult
}

// ETA estimates the remaining time from the average conversation so far.
// It returns false before there is enough completed work to extrapolate from.
func (e ProgressEvent) ETA() (time.Duration, bool) {
	if e.Done <= 0 || e.Done >= e.Total || e.Elapsed <= 0 {
		return 0, false
	}
	perItem := e.Elapsed / time.Duration(e.Done)
	return perItem * time.Duration(e.Total-e.Done), true
}

// workItem pairs a conversation with the provider that can read it.
type workItem struct {
	provider provider.Provider
	summary  provider.ConversationSummary
}

// ConversationResult records what happened to a single conversation.
type ConversationResult struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	Points   int    `json:"points"`
	Skipped  bool   `json:"skipped"`
	Error    string `json:"error,omitempty"`
}

// Result summarises one ingestion pass.
type Result struct {
	Collection    string `json:"collection"`
	Endpoint      string `json:"endpoint"`
	DryRun        bool   `json:"dry_run"`
	Conversations int    `json:"conversations"`
	Published     int    `json:"published"`
	Skipped       int    `json:"skipped"`
	Points        int    `json:"points"`
	Failed        int    `json:"failed"`
	Duration      string `json:"duration"`
	// PointsBefore and PointsAfter are the collection's totals either side of
	// the pass. Both are -1 when the count could not be read.
	PointsBefore int64                `json:"points_before"`
	PointsAfter  int64                `json:"points_after"`
	Details      []ConversationResult `json:"details,omitempty"`
}

// Delta reports the change in stored points, and whether it is known.
func (r *Result) Delta() (int64, bool) {
	if r.PointsBefore < 0 || r.PointsAfter < 0 {
		return 0, false
	}
	return r.PointsAfter - r.PointsBefore, true
}

// Run publishes every changed conversation into the configured collection.
func Run(ctx context.Context, cfg *Config, opts Options) (*Result, error) {
	client := newQdrantClient(cfg)
	if !opts.DryRun {
		if err := client.EnsureCollection(ctx, cfg.Collection); err != nil {
			return nil, err
		}
	}

	result := &Result{
		Collection:   cfg.Collection,
		Endpoint:     cfg.QdrantURL.Redacted(),
		DryRun:       opts.DryRun,
		PointsBefore: remotePoints(ctx, client, cfg.Collection),
		PointsAfter:  -1,
	}

	// The work is gathered up front so progress has a denominator to report
	// against; listing is cheap next to reading the transcripts themselves.
	work, listErrors := gatherWork(opts)
	result.Conversations = len(work)
	for _, failure := range listErrors {
		result.Failed++
		result.Details = append(result.Details, failure)
	}

	store := loadOffsets(cfg.OffsetsFile)
	started := time.Now()

	for _, item := range work {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		detail := ingestOne(ctx, client, cfg, store, opts, item.provider, item.summary)
		applyResult(result, detail)

		if opts.OnProgress != nil {
			opts.OnProgress(ProgressEvent{
				Done:    result.Published + result.Skipped + result.Failed - len(listErrors),
				Total:   len(work),
				Points:  result.Points,
				Elapsed: time.Since(started),
				Result:  detail,
			})
		}
	}

	sort.Slice(result.Details, func(i, j int) bool {
		if result.Details[i].Provider != result.Details[j].Provider {
			return result.Details[i].Provider < result.Details[j].Provider
		}
		return result.Details[i].ID < result.Details[j].ID
	})

	if !opts.DryRun {
		if err := store.save(); err != nil {
			return result, err
		}
		result.PointsAfter = remotePoints(ctx, client, cfg.Collection)
	}
	result.Duration = time.Since(started).Round(time.Millisecond).String()
	return result, nil
}

// gatherWork lists every conversation to be considered, and reports providers
// that could not be listed rather than aborting the pass.
func gatherWork(opts Options) ([]workItem, []ConversationResult) {
	providers, err := provider.Select(opts.Provider)
	if err != nil {
		return nil, []ConversationResult{{Error: err.Error()}}
	}

	var work []workItem
	var failures []ConversationResult
	for _, p := range providers {
		summaries, err := p.ListConversations(provider.HistoryOptions{Since: opts.Since})
		if err != nil {
			failures = append(failures, ConversationResult{
				Provider: p.Name(),
				Error:    fmt.Sprintf("listing conversations: %v", err),
			})
			continue
		}
		for _, summary := range summaries {
			work = append(work, workItem{provider: p, summary: summary})
		}
	}
	return work, failures
}

// remotePoints reads the collection's point total, returning -1 when it cannot
// be determined so a missing figure is never mistaken for an empty collection.
func remotePoints(ctx context.Context, client *qdrantClient, collection string) int64 {
	info, err := client.GetCollection(ctx, collection)
	if err != nil || info == nil {
		return -1
	}
	return info.PointsCount
}

// ingestOne publishes a single conversation, reporting rather than returning
// an error so one bad transcript cannot end the pass.
func ingestOne(
	ctx context.Context,
	client *qdrantClient,
	cfg *Config,
	store *offsetStore,
	opts Options,
	p provider.Provider,
	summary provider.ConversationSummary,
) ConversationResult {
	out := ConversationResult{Provider: summary.Provider, ID: summary.ID, Title: summary.Title}
	key := offsetKey(summary.Provider, summary.ID)

	if !opts.Force && store.upToDate(key, cfg.Collection, summary.UpdatedAt, summary.TotalSizeBytes) {
		out.Skipped = true
		return out
	}

	detail, err := p.GetConversation(summary.ID)
	if err != nil {
		out.Error = fmt.Sprintf("reading conversation: %v", err)
		return out
	}
	if detail == nil {
		out.Error = "conversation disappeared while reading"
		return out
	}

	points := turnsToPoints(cfg, summary, detail.Turns)
	out.Points = len(points)
	if len(points) == 0 {
		// Nothing to publish, but the state was still examined at this size.
		store.record(key, cfg.Collection, summary.UpdatedAt, summary.TotalSizeBytes, 0)
		return out
	}
	if opts.DryRun {
		return out
	}

	if err := client.Upsert(ctx, cfg.Collection, points); err != nil {
		out.Error = err.Error()
		return out
	}
	store.record(key, cfg.Collection, summary.UpdatedAt, summary.TotalSizeBytes, len(points))
	return out
}

func applyResult(result *Result, detail ConversationResult) {
	switch {
	case detail.Error != "":
		result.Failed++
	case detail.Skipped:
		result.Skipped++
	default:
		result.Published++
		result.Points += detail.Points
	}

	// Skipped conversations are the common case; recording them would bury the
	// work that actually happened.
	if !detail.Skipped {
		result.Details = append(result.Details, detail)
	}
}

// Status reports the destination's health and what the local offset store believes.
type Status struct {
	Configured    bool   `json:"configured"`
	Endpoint      string `json:"endpoint,omitempty"`
	Collection    string `json:"collection,omitempty"`
	Reachable     bool   `json:"reachable"`
	CollectionURL string `json:"-"`
	Health        string `json:"health,omitempty"`
	RemotePoints  int64  `json:"remote_points"`
	TrackedLocal  int    `json:"tracked_conversations"`
	TrackedPoints int    `json:"tracked_points"`
	OffsetsFile   string `json:"offsets_file,omitempty"`
	LastIngestAt  string `json:"last_ingest_at,omitempty"`
	Error         string `json:"error,omitempty"`
}

// GetStatus inspects the configured destination and the local offset store.
func GetStatus(ctx context.Context, cfg *Config) *Status {
	status := &Status{
		Configured:  true,
		Endpoint:    cfg.QdrantURL.Redacted(),
		Collection:  cfg.Collection,
		OffsetsFile: cfg.OffsetsFile,
	}

	store := loadOffsets(cfg.OffsetsFile)
	var newest time.Time
	for _, record := range store.Records {
		if record.Collection != cfg.Collection {
			continue
		}
		status.TrackedLocal++
		status.TrackedPoints += record.Points
		if record.IngestedAt.After(newest) {
			newest = record.IngestedAt
		}
	}
	if !newest.IsZero() {
		status.LastIngestAt = newest.UTC().Format(time.RFC3339)
	}

	info, err := newQdrantClient(cfg).GetCollection(ctx, cfg.Collection)
	switch {
	case err != nil:
		status.Error = err.Error()
	case info == nil:
		status.Reachable = true
		status.Health = "absent"
	default:
		status.Reachable = true
		status.Health = info.Status
		status.RemotePoints = info.PointsCount
	}
	return status
}
