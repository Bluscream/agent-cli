package ingest

import (
	"strings"
	"testing"
)

func TestRedactorGenericPatterns(t *testing.T) {
	r := &Redactor{
		patterns: buildGenericRedactionPatterns(),
	}

	tests := []struct {
		input string
		want  string
	}{
		{
			input: "Phone: 01789196633",
			want:  "Phone: [REDACTED_PHONE]",
		},
		{
			input: "Dial +49 178 9196633 for help",
			want:  "Dial [REDACTED_PHONE] for help",
		},
		{
			input: "API key is " + "sk-" + "12345678901234567890abcdef",
			want:  "API key is [REDACTED_API_KEY]",
		},
		{
			input: "GitHub token: " + "ghp_" + "123456789012345678901234567890123456",
			want:  "GitHub token: [REDACTED_TOKEN]",
		},
		{
			input: "password: \"supersecretphrase123\"",
			want:  "password: \"[REDACTED_PASSWORD]\"",
		},
		{
			input: "Optimizations and automations should not be broken",
			want:  "Optimizations and automations should not be broken",
		},
	}

	for _, tt := range tests {
		got := r.Redact(tt.input)
		if got != tt.want {
			t.Errorf("Redact(%q) =\n  %q\nwant:\n  %q", tt.input, got, tt.want)
		}
	}
}

func TestRedactorDiscoveredSecrets(t *testing.T) {
	r := &Redactor{
		secrets: []string{"custom_secret_key_999", "confidential_word"},
	}

	input := "Connecting with custom_secret_key_999 and confidential_word to server"
	got := r.Redact(input)
	want := "Connecting with [REDACTED_SECRET] and [REDACTED_SECRET] to server"

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if strings.Contains(got, "custom_secret") || strings.Contains(got, "confidential") {
		t.Errorf("failed to redact discovered secret: %q", got)
	}
}
