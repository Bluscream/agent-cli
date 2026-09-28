package provider

import (
	"fmt"
	"strings"
	"time"
)

// PurgeTarget names what a purge should erase.
type PurgeTarget struct {
	// Provider restricts to one provider; empty means every provider.
	Provider string
	// ID restricts to one conversation; empty means every conversation of the
	// selected providers.
	ID string
}

// Describe renders the target for a confirmation prompt, so what the user is
// asked about and what is deleted come from the same value.
func (t PurgeTarget) Describe() string {
	switch {
	case t.ID != "" && t.Provider != "":
		return fmt.Sprintf("conversation %s of %s", t.ID, t.Provider)
	case t.ID != "":
		return fmt.Sprintf("conversation %s", t.ID)
	case t.Provider != "":
		return fmt.Sprintf("every conversation of %s", t.Provider)
	default:
		return "every conversation of every provider"
	}
}

// RunPurge erases the targeted conversations and summarises what went.
//
// A provider that does not implement ConversationPurger is named in
// Unsupported rather than skipped silently: "delete everything" must not
// quietly mean "delete everything from the providers that happened to support
// it".
func RunPurge(target PurgeTarget, opts PurgeOptions) (*PurgeSummary, error) {
	started := time.Now()
	summary := &PurgeSummary{DryRun: opts.DryRun, StartedAt: started}

	if target.ID != "" {
		located, err := Locate(target.Provider, target.ID)
		if err != nil {
			return nil, err
		}
		purger, ok := located.Provider.(ConversationPurger)
		if !ok {
			return nil, fmt.Errorf("%s cannot delete conversations", located.Provider.DisplayName())
		}
		purge, err := purger.PurgeConversation(located.Summary.ID, opts)
		if err != nil {
			return nil, err
		}
		summary.Add(*purge)
		summary.Duration = time.Since(started).Round(time.Millisecond).String()
		return summary, nil
	}

	providers, err := Select(target.Provider)
	if err != nil {
		return nil, err
	}

	var failures []string
	for _, p := range providers {
		purger, ok := p.(ConversationPurger)
		if !ok {
			summary.Unsupported = append(summary.Unsupported, p.Name())
			continue
		}
		purges, err := purger.PurgeAllConversations(opts)
		if err != nil {
			// One provider refusing — a running IDE, an unreadable database —
			// must not abandon the others, but it is still a failure of the
			// request as asked, so it is reported at the end.
			failures = append(failures, fmt.Sprintf("%s: %v", p.Name(), err))
			continue
		}
		for _, purge := range purges {
			summary.Add(purge)
		}
	}

	summary.Sort()
	summary.Duration = time.Since(started).Round(time.Millisecond).String()
	if len(failures) > 0 {
		return summary, fmt.Errorf("could not purge %s", strings.Join(failures, "; "))
	}
	return summary, nil
}
