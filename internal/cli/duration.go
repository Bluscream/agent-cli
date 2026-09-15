package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseSinceDuration parses relative duration strings like "2w", "1d", "3h", "30m", "1 day", "2 weeks"
func ParseSinceDuration(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	clean := strings.ToLower(strings.TrimSpace(s))

	// Handle natural phrases
	clean = strings.ReplaceAll(clean, "weeks", "w")
	clean = strings.ReplaceAll(clean, "week", "w")
	clean = strings.ReplaceAll(clean, "days", "d")
	clean = strings.ReplaceAll(clean, "day", "d")
	clean = strings.ReplaceAll(clean, "hours", "h")
	clean = strings.ReplaceAll(clean, "hour", "h")
	clean = strings.ReplaceAll(clean, "mins", "m")
	clean = strings.ReplaceAll(clean, "min", "m")
	clean = strings.ReplaceAll(clean, "minutes", "m")
	clean = strings.ReplaceAll(clean, "minute", "m")
	clean = strings.ReplaceAll(clean, " ", "")

	now := time.Now()

	// Check if ends with 'w', 'd', 'h', 'm', 's'
	if strings.HasSuffix(clean, "w") {
		numStr := strings.TrimSuffix(clean, "w")
		n, err := strconv.Atoi(numStr)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid duration number: %s", numStr)
		}
		return now.AddDate(0, 0, -n*7), nil
	}
	if strings.HasSuffix(clean, "d") {
		numStr := strings.TrimSuffix(clean, "d")
		n, err := strconv.Atoi(numStr)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid duration number: %s", numStr)
		}
		return now.AddDate(0, 0, -n), nil
	}

	// Standard Go time.ParseDuration for h, m, s
	dur, err := time.ParseDuration(clean)
	if err == nil {
		return now.Add(-dur), nil
	}

	// Try RFC3339 or ISO8601 absolute date
	if t, err := time.Parse("2006-01-02", clean); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, clean); err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("unknown duration format %q (examples: '2w', '1d', '3h', '30m')", s)
}
