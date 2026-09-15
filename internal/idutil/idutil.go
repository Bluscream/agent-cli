package idutil

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// ShortID returns a deterministic 8-character hex hash of any raw ID string.
// If raw is empty, it returns an empty string.
func ShortID(raw string) string {
	if raw == "" {
		return ""
	}
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])[:8]
}

// Match checks whether a query matches a raw ID or its ShortID.
// It supports:
// 1. Exact raw ID match (case-insensitive)
// 2. Exact ShortID match (case-insensitive)
// 3. Prefix match on raw ID (e.g. UUID prefix like "98256169")
// 4. Prefix match on ShortID (if query length >= 4)
func Match(query, raw string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" || raw == "" {
		return false
	}
	r := strings.ToLower(strings.TrimSpace(raw))
	s := ShortID(raw)

	if r == q || s == q {
		return true
	}
	if strings.HasPrefix(r, q) {
		return true
	}
	if len(q) >= 4 && strings.HasPrefix(s, q) {
		return true
	}
	return false
}
