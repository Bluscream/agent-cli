package provider

import (
	"fmt"
	"strings"
	"sync"
)

var (
	registryMu sync.RWMutex
	providers  = make(map[string]Provider)
	order      []string
)

func Register(p Provider) {
	registryMu.Lock()
	defer registryMu.Unlock()
	name := strings.ToLower(p.Name())
	if _, exists := providers[name]; !exists {
		order = append(order, name)
	}
	providers[name] = p
}

func Get(name string) (Provider, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	n := strings.ToLower(name)
	switch n {
	case "claude", "claude-desktop", "claudedesktop":
		n = "claude"
	case "antigravity", "antigravity-ide", "antigravityide", "gemini":
		n = "antigravity"
	case "codex", "codex-desktop", "chatgpt":
		n = "codex"
	}
	p, ok := providers[n]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %q (available: %s)", name, strings.Join(order, ", "))
	}
	return p, nil
}

func All() []Provider {
	registryMu.RLock()
	defer registryMu.RUnlock()
	res := make([]Provider, 0, len(order))
	for _, name := range order {
		res = append(res, providers[name])
	}
	return res
}

func Names() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	cp := make([]string, len(order))
	copy(cp, order)
	return cp
}

// Select resolves a provider name to the providers to act on: one when named,
// all of them when not. It replaces the block every command had written out
// itself, and it errors when nothing is registered rather than quietly
// succeeding over an empty list.
func Select(name string) ([]Provider, error) {
	if strings.TrimSpace(name) == "" {
		all := All()
		if len(all) == 0 {
			return nil, fmt.Errorf("no providers are registered")
		}
		return all, nil
	}
	p, err := Get(name)
	if err != nil {
		return nil, err
	}
	return []Provider{p}, nil
}

// Located is a conversation together with the provider that holds it.
type Located struct {
	Provider Provider
	Summary  ConversationSummary
}

// Locate finds the one conversation matching id across the selected providers.
//
// An id that matches conversations in more than one provider is an error naming
// them, not a silent pick. Four call sites resolved conversations this way and
// only one of them checked: the others took whichever provider happened to be
// registered first, so `ai log <prefix>` could print a different conversation
// than `ai audit <prefix>` for the same argument.
func Locate(providerName, id string) (*Located, error) {
	providers, err := Select(providerName)
	if err != nil {
		return nil, err
	}

	var found []Located
	for _, p := range providers {
		summary, err := Find(p, id)
		if err != nil {
			// Not found in this provider is the normal case when searching
			// several; only a real listing failure is worth reporting, and it
			// is reported by whichever provider the caller then acts on.
			continue
		}
		found = append(found, Located{Provider: p, Summary: *summary})
	}

	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no conversation matching %q", id)
	case 1:
		return &found[0], nil
	}

	names := make([]string, 0, len(found))
	for _, f := range found {
		names = append(names, fmt.Sprintf("%s (%s)", f.Provider.Name(), f.Summary.ID))
	}
	return nil, fmt.Errorf("%q matches conversations in several providers: %s; narrow it with -p or use the full id",
		id, strings.Join(names, ", "))
}
