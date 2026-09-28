package ingest

import (
	"context"
	"fmt"
	"sort"
	"time"

	"agentcli.local/ai/internal/provider"
)

// VerifyOptions controls one verification pass.
type VerifyOptions struct {
	Provider string // restrict to one provider; empty means all
	Since    time.Time
	// Deep re-reads each transcript and compares turn by turn. Without it the
	// pass compares point counts, which needs no transcript parsing and is
	// what catches a conversation that was never published or was published
	// and then lost.
	Deep bool
	// OnProgress, when set, is called once per conversation.
	OnProgress func(VerifyProgress)
}

// VerifyProgress reports how far a verification pass has got.
type VerifyProgress struct {
	Done  int
	Total int
	Issue *ConversationCheck
}

// CheckStatus is the outcome for one conversation.
type CheckStatus string

const (
	// CheckOK means the stored points match what the transcript holds.
	CheckOK CheckStatus = "ok"
	// CheckMissing means the conversation has publishable turns and nothing
	// at all is stored for it.
	CheckMissing CheckStatus = "missing"
	// CheckIncomplete means fewer points are stored than the transcript
	// yields — a pass that failed partway, or turns added since.
	CheckIncomplete CheckStatus = "incomplete"
	// CheckStale means more points are stored than the transcript yields.
	// Turns do not disappear from a transcript, so this normally means the
	// transcript was truncated or rewritten under a reused id.
	CheckStale CheckStatus = "stale"
	// CheckEmpty means the conversation has no publishable turns, so storing
	// nothing is correct.
	CheckEmpty CheckStatus = "empty"
	// CheckError means the conversation could not be checked.
	CheckError CheckStatus = "error"
)

// ConversationCheck is one conversation's verification result.
type ConversationCheck struct {
	Provider string      `json:"provider"`
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	Status   CheckStatus `json:"status"`
	Expected int         `json:"expected_points"`
	Stored   int64       `json:"stored_points"`
	// MissingContent counts turns whose text is absent from the collection.
	// Only filled by a deep pass.
	MissingContent int    `json:"missing_content,omitempty"`
	Error          string `json:"error,omitempty"`
}

// OK reports whether this conversation needs no action.
func (c ConversationCheck) OK() bool {
	return c.Status == CheckOK || c.Status == CheckEmpty
}

// VerifyReport summarises a verification pass.
type VerifyReport struct {
	Collection string `json:"collection"`
	Endpoint   string `json:"endpoint"`
	Deep       bool   `json:"deep"`
	// Local is the number of conversations found on this machine.
	Local int `json:"local_conversations"`
	// RemoteSessions is the number of distinct session ids in the collection,
	// or -1 when it could not be enumerated.
	RemoteSessions int `json:"remote_sessions"`
	// Orphans are session ids stored remotely with no local conversation.
	// These are what `ai ingest --cleanup` would remove.
	Orphans []string `json:"orphans,omitempty"`

	OK         int `json:"ok"`
	Empty      int `json:"empty"`
	Missing    int `json:"missing"`
	Incomplete int `json:"incomplete"`
	Stale      int `json:"stale"`
	Failed     int `json:"failed"`

	ExpectedPoints int   `json:"expected_points"`
	StoredPoints   int64 `json:"stored_points"`
	// TotalPoints is the collection's own total, which includes points from
	// other hosts and so is not expected to equal StoredPoints.
	TotalPoints int64 `json:"collection_points"`

	Duration string `json:"duration"`
	// Issues lists only the conversations that need attention. A pass over a
	// healthy collection returns none.
	Issues []ConversationCheck `json:"issues,omitempty"`
}

// Healthy reports whether every conversation verified and nothing is orphaned.
func (r *VerifyReport) Healthy() bool {
	return r.Missing == 0 && r.Incomplete == 0 && r.Stale == 0 && r.Failed == 0 && len(r.Orphans) == 0
}

// Verify checks that every local conversation is stored in the collection and
// retrievable from it. It writes nothing.
//
// The comparison is per conversation rather than against the collection total,
// because a total can match while individual conversations are missing: one
// conversation over-published and another absent cancel out.
func Verify(ctx context.Context, cfg *Config, opts VerifyOptions) (*VerifyReport, error) {
	client := newQdrantClient(cfg)
	if err := ensureReachable(ctx, cfg, client); err != nil {
		return nil, err
	}

	report := &VerifyReport{
		Collection:     cfg.Collection,
		Endpoint:       cfg.QdrantURL.Redacted(),
		Deep:           opts.Deep,
		RemoteSessions: -1,
		TotalPoints:    remotePoints(ctx, client, cfg.Collection),
	}
	started := time.Now()

	work, listErrors := gatherWork(Options{Provider: opts.Provider, Since: opts.Since})
	report.Local = len(work)
	for _, failure := range listErrors {
		report.Failed++
		report.Issues = append(report.Issues, ConversationCheck{
			Provider: failure.Provider, Status: CheckError, Error: failure.Error,
		})
	}

	local := make(map[string]bool, len(work))
	for i, item := range work {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		local[item.summary.ID] = true

		check := verifyOne(ctx, client, cfg, opts, item.provider, item.summary)
		applyCheck(report, check)

		if opts.OnProgress != nil {
			event := VerifyProgress{Done: i + 1, Total: len(work)}
			if !check.OK() {
				event.Issue = &check
			}
			opts.OnProgress(event)
		}
	}

	// Orphans are only meaningful for a pass that looked at every provider and
	// every date: narrowing the local side would report everything outside the
	// filter as orphaned.
	if opts.Provider == "" && opts.Since.IsZero() {
		sessions, err := client.FacetValues(ctx, cfg.Collection, "session_id", maxFacetSessions)
		if err == nil {
			report.RemoteSessions = len(sessions)
			for _, id := range sessions {
				if !local[id] {
					report.Orphans = append(report.Orphans, id)
				}
			}
			sort.Strings(report.Orphans)
		}
	}

	sort.Slice(report.Issues, func(i, j int) bool {
		if report.Issues[i].Provider != report.Issues[j].Provider {
			return report.Issues[i].Provider < report.Issues[j].Provider
		}
		return report.Issues[i].ID < report.Issues[j].ID
	})
	report.Duration = time.Since(started).Round(time.Millisecond).String()
	return report, nil
}

// verifyOne compares one conversation against what is stored for it.
func verifyOne(
	ctx context.Context,
	client *qdrantClient,
	cfg *Config,
	opts VerifyOptions,
	p provider.Provider,
	summary provider.ConversationSummary,
) ConversationCheck {
	check := ConversationCheck{Provider: summary.Provider, ID: summary.ID, Title: summary.Title}

	filter := buildFilter(QueryOptions{Provider: summary.Provider, SessionID: summary.ID})
	stored, err := client.Count(ctx, cfg.Collection, filter)
	if err != nil {
		check.Status = CheckError
		check.Error = fmt.Sprintf("counting stored points: %v", err)
		return check
	}
	check.Stored = stored

	detail, err := p.GetConversation(summary.ID)
	if err != nil {
		check.Status = CheckError
		check.Error = fmt.Sprintf("reading conversation: %v", err)
		return check
	}
	if detail == nil {
		check.Status = CheckError
		check.Error = "conversation disappeared while reading"
		return check
	}

	points := turnsToPoints(cfg, summary, detail.Turns)
	check.Expected = len(points)

	switch {
	case len(points) == 0 && stored == 0:
		check.Status = CheckEmpty
		return check
	case stored == 0:
		check.Status = CheckMissing
		return check
	case stored < int64(len(points)):
		check.Status = CheckIncomplete
	case stored > int64(len(points)):
		check.Status = CheckStale
	default:
		check.Status = CheckOK
	}

	if opts.Deep {
		missing, err := missingContent(ctx, client, cfg, summary, points)
		if err != nil {
			check.Status = CheckError
			check.Error = err.Error()
			return check
		}
		check.MissingContent = missing
		if missing > 0 && check.Status == CheckOK {
			// The counts agreed but the text did not, which is the failure a
			// count-only pass cannot see.
			check.Status = CheckIncomplete
		}
	}
	return check
}

// missingContent reads the conversation's stored points back and reports how
// many of the expected turns are absent or hold different text. This is the
// check that answers "retrievable", as opposed to merely "counted".
func missingContent(
	ctx context.Context,
	client *qdrantClient,
	cfg *Config,
	summary provider.ConversationSummary,
	expected []Point,
) (int, error) {
	filter := buildFilter(QueryOptions{Provider: summary.Provider, SessionID: summary.ID})

	storedByID := make(map[string]Payload, len(expected))
	var offset any
	for range maxOrderedPages {
		page, err := client.Scroll(ctx, cfg.Collection, scrollRequest{
			Filter: filter, Limit: scrollPageSize, Offset: offset, WithPayload: true,
		})
		if err != nil {
			return 0, fmt.Errorf("reading stored turns: %w", err)
		}
		for _, point := range page.Points {
			storedByID[point.ID] = point.Payload
		}
		if page.NextPageOffset == nil || len(page.Points) == 0 {
			break
		}
		offset = page.NextPageOffset
	}

	missing := 0
	for _, want := range expected {
		got, found := storedByID[want.ID]
		if !found || got.Content != want.Payload.Content || got.Thinking != want.Payload.Thinking {
			missing++
		}
	}
	return missing, nil
}

func applyCheck(report *VerifyReport, check ConversationCheck) {
	report.ExpectedPoints += check.Expected
	report.StoredPoints += check.Stored

	switch check.Status {
	case CheckOK:
		report.OK++
	case CheckEmpty:
		report.Empty++
	case CheckMissing:
		report.Missing++
	case CheckIncomplete:
		report.Incomplete++
	case CheckStale:
		report.Stale++
	case CheckError:
		report.Failed++
	}

	// Only problems are listed: a healthy pass over hundreds of conversations
	// should print a summary, not hundreds of "ok" rows.
	if !check.OK() {
		report.Issues = append(report.Issues, check)
	}
}
