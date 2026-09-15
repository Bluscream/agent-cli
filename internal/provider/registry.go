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
