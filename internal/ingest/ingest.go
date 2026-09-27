package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	Progress io.Writer // optional per-conversation progress output
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
	Collection    string               `json:"collection"`
	Endpoint      string               `json:"endpoint"`
	DryRun        bool                 `json:"dry_run"`
	Conversations int                  `json:"conversations"`
	Published     int                  `json:"published"`
	Skipped       int                  `json:"skipped"`
	Points        int                  `json:"points"`
	Failed        int                  `json:"failed"`
	Duration      string               `json:"duration"`
	Details       []ConversationResult `json:"details,omitempty"`
}

// Run publishes every changed conversation into the configured collection.
func Run(ctx context.Context, cfg *Config, opts Options) (*Result, error) {
	providers, err := selectProviders(opts.Provider)
	if err != nil {
		return nil, err
	}

	client := newQdrantClient(cfg)
	if !opts.DryRun {
		if err := client.EnsureCollection(ctx, cfg.Collection); err != nil {
			return nil, err
		}
	}

	store := loadOffsets(cfg.OffsetsFile)
	started := time.Now()
	result := &Result{
		Collection: cfg.Collection,
		Endpoint:   cfg.QdrantURL.Redacted(),
		DryRun:     opts.DryRun,
	}

	for _, p := range providers {
		summaries, err := p.ListConversations(provider.HistoryOptions{Since: opts.Since})
		if err != nil {
			// One unavailable provider must not abort the others.
			result.Failed++
			result.Details = append(result.Details, ConversationResult{
				Provider: p.Name(),
				Error:    fmt.Sprintf("listing conversations: %v", err),
			})
			continue
		}

		for _, summary := range summaries {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			result.Conversations++
			detail := ingestOne(ctx, client, cfg, store, opts, p, summary)
			applyResult(result, detail, opts.Progress)
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
	}
	result.Duration = time.Since(started).Round(time.Millisecond).String()
	return result, nil
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

func applyResult(result *Result, detail ConversationResult, progress io.Writer) {
	switch {
	case detail.Error != "":
		result.Failed++
	case detail.Skipped:
		result.Skipped++
	default:
		result.Published++
		result.Points += detail.Points
	}

	// Skipped conversations are the common case; listing them would bury the
	// work that actually happened.
	if !detail.Skipped {
		result.Details = append(result.Details, detail)
		if progress != nil {
			if detail.Error != "" {
				fmt.Fprintf(progress, "  failed   %s %.8s  %s\n", detail.Provider, detail.ID, detail.Error)
			} else {
				fmt.Fprintf(progress, "  %-8s %s %.8s  %d points  %s\n",
					publishedVerb(result.DryRun), detail.Provider, detail.ID, detail.Points, detail.Title)
			}
		}
	}
}

func publishedVerb(dryRun bool) string {
	if dryRun {
		return "would"
	}
	return "sent"
}

func selectProviders(name string) ([]provider.Provider, error) {
	if name == "" {
		all := provider.All()
		if len(all) == 0 {
			return nil, errors.New("no providers are registered")
		}
		return all, nil
	}
	p, err := provider.Get(name)
	if err != nil {
		return nil, err
	}
	return []provider.Provider{p}, nil
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
