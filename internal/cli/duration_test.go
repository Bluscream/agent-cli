package cli

import (
	"testing"
	"time"
)

func TestParseSinceDuration(t *testing.T) {
	now := time.Now()

	tests := []struct {
		input        string
		expectMinDur time.Duration
		expectMaxDur time.Duration
	}{
		{"2w", 13 * 24 * time.Hour, 15 * 24 * time.Hour},
		{"1d", 23 * time.Hour, 25 * time.Hour},
		{"3h", 2*time.Hour + 50*time.Minute, 3*time.Hour + 10*time.Minute},
		{"30m", 29 * time.Minute, 31 * time.Minute},
		{"1 day", 23 * time.Hour, 25 * time.Hour},
		{"2 weeks", 13 * 24 * time.Hour, 15 * 24 * time.Hour},
	}

	for _, tt := range tests {
		parsed, err := ParseSinceDuration(tt.input)
		if err != nil {
			t.Fatalf("ParseSinceDuration(%q) returned error: %v", tt.input, err)
		}
		diff := now.Sub(parsed)
		if diff < tt.expectMinDur || diff > tt.expectMaxDur {
			t.Errorf("ParseSinceDuration(%q) = %v (diff: %v), expected between %v and %v",
				tt.input, parsed, diff, tt.expectMinDur, tt.expectMaxDur)
		}
	}
}
