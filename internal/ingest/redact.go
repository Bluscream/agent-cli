package ingest

import (
	"bufio"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type patternRule struct {
	re   *regexp.Regexp
	repl string
}

// Redactor scrubs sensitive personal information, credentials, phone numbers,
// and environment secrets from text before ingestion.
// No sensitive values or names are hardcoded in the codebase: all redactions
// use generic token/phone/credential patterns, system identity discovery,
// local environment files, and user-defined local config (~/.config/agent-cli/redact.words).
type Redactor struct {
	secrets  []string
	patterns []patternRule
}

// NewRedactor initializes generic redaction patterns and discovers local secrets
// from the operating system, environment, and user configuration files.
func NewRedactor() *Redactor {
	r := &Redactor{
		secrets:  discoverLocalSecrets(),
		patterns: buildGenericRedactionPatterns(),
	}
	return r
}

func discoverLocalSecrets() []string {
	seen := make(map[string]struct{})

	ignoredValues := map[string]struct{}{
		"true": {}, "false": {}, "192.168.2.1": {}, "192.168.2.11": {}, "192.168.2.10": {},
		"192.168.2.4": {}, "sysadmin": {}, "root": {}, "bazzite": {}, "ai_history": {},
		"ai_history_v2": {}, "none": {}, "null": {}, "localhost": {}, "127.0.0.1": {},
	}

	scanUserIdentity(seen)
	scanConfigFileWords(seen)
	scanEnvironmentFiles(seen, ignoredValues)
	scanProcessEnvironment(seen, ignoredValues)

	// Sort descending by length so longer substrings match before shorter prefixes
	secrets := make([]string, 0, len(seen))
	for s := range seen {
		secrets = append(secrets, s)
	}
	sort.Slice(secrets, func(i, j int) bool {
		return len(secrets[i]) > len(secrets[j])
	})

	return secrets
}

func scanUserIdentity(seen map[string]struct{}) {
	u, err := user.Current()
	if err != nil {
		return
	}
	if u.Username != "" && len(u.Username) >= 3 && u.Username != "root" {
		seen[u.Username] = struct{}{}
	}
	if u.Name != "" && len(u.Name) >= 3 {
		for _, part := range strings.Fields(u.Name) {
			if len(part) >= 3 {
				seen[part] = struct{}{}
			}
		}
	}
}

func scanConfigFileWords(seen map[string]struct{}) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	wordFiles := []string{
		filepath.Join(home, ".config", "agent-cli", "redact.words"),
		filepath.Join(home, ".config", "agent-cli", "redact.conf"),
	}
	for _, wf := range wordFiles {
		f, err := os.Open(wf)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			w := strings.TrimSpace(sc.Text())
			if w != "" && !strings.HasPrefix(w, "#") && len(w) >= 3 {
				seen[w] = struct{}{}
			}
		}
		_ = f.Close()
	}
}

func scanEnvironmentFiles(seen map[string]struct{}, ignored map[string]struct{}) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	skipFiles := []string{"10-path", "10-hardware", "10-cargo", "10-unity", "30-ai"}
	envDir := filepath.Join(home, ".config", "environment.d")
	entries, _ := os.ReadDir(envDir)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".conf") {
			continue
		}
		if hasPrefixAny(entry.Name(), skipFiles) {
			continue
		}

		fullPath := filepath.Join(envDir, entry.Name())
		f, err := os.Open(fullPath)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if _, v, ok := strings.Cut(line, "="); ok {
				val := strings.Trim(v, "\"' \t\r\n")
				if isSecretCandidate(val, ignored) {
					seen[val] = struct{}{}
				}
			}
		}
		_ = f.Close()
	}
}

func scanProcessEnvironment(seen map[string]struct{}, ignored map[string]struct{}) {
	if extra := os.Getenv("AI_INGEST_REDACT_WORDS"); extra != "" {
		for _, w := range strings.Split(extra, ",") {
			w = strings.TrimSpace(w)
			if len(w) >= 3 {
				seen[w] = struct{}{}
			}
		}
	}

	sensitiveKeyIndicators := []string{
		"KEY", "SECRET", "TOKEN", "PASSWORD", "PASSWD", "AUTH", "CREDENTIAL", "APIKEY",
	}
	for _, env := range os.Environ() {
		k, v, ok := strings.Cut(env, "=")
		if !ok {
			continue
		}
		upperK := strings.ToUpper(k)
		for _, indicator := range sensitiveKeyIndicators {
			if strings.Contains(upperK, indicator) {
				val := strings.Trim(v, "\"' \t\r\n")
				if isSecretCandidate(val, ignored) {
					seen[val] = struct{}{}
				}
				break
			}
		}
	}
}

func hasPrefixAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func isSecretCandidate(val string, ignored map[string]struct{}) bool {
	if len(val) < 4 {
		return false
	}
	if strings.HasPrefix(val, "http://") || strings.HasPrefix(val, "https://") || strings.HasPrefix(val, "/") {
		return false
	}
	if _, bad := ignored[strings.ToLower(val)]; bad {
		return false
	}
	return true
}

func buildGenericRedactionPatterns() []patternRule {
	return []patternRule{
		// Generic phone numbers (international, national mobile and landline)
		{
			re:   regexp.MustCompile(`(?:\+49[\s\-\/]*|0049[\s\-\/]*|\b0)(?:1[567][0-9]|2\d{1,3}|3\d{1,3}|4\d{1,3}|5\d{1,3}|6\d{1,3}|7\d{1,3}|8\d{1,3}|9\d{1,3})(?:[\s\-\/]?[0-9]){5,9}\b`),
			repl: "[REDACTED_PHONE]",
		},

		// Generic API tokens & credential formats
		{re: regexp.MustCompile(`\bsk-[a-zA-Z0-9_\-]{20,}\b`), repl: "[REDACTED_API_KEY]"},
		{re: regexp.MustCompile(`\bsk-ant-[a-zA-Z0-9_\-]{20,}\b`), repl: "[REDACTED_API_KEY]"},
		{re: regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[a-zA-Z0-9]{20,}\b`), repl: "[REDACTED_TOKEN]"},
		{re: regexp.MustCompile(`\bgithub_pat_[a-zA-Z0-9_]{20,}\b`), repl: "[REDACTED_TOKEN]"},
		{re: regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\b`), repl: "[REDACTED_JWT]"},
		{re: regexp.MustCompile(`\b[M-Z][A-Za-z0-9_\-]{23,28}\.[A-Za-z0-9_\-]{6}\.[A-Za-z0-9_\-]{27,38}\b`), repl: "[REDACTED_DISCORD_TOKEN]"},
		{re: regexp.MustCompile(`\btskey-(?:auth|k)-[a-zA-Z0-9_\-]{20,}\b`), repl: "[REDACTED_KEY]"},
		{re: regexp.MustCompile(`\bxox[baprs]-[0-9]{10,}-[a-zA-Z0-9]{20,}\b`), repl: "[REDACTED_SLACK_TOKEN]"},

		// Inline passwords in logs/code: e.g. password = "...", pwd: "..."
		{
			re:   regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret)\s*[:=]\s*["']?([^\s"';,]{6,})["']?`),
			repl: "$1: \"[REDACTED_PASSWORD]\"",
		},

		// Emails (excluding standard public/mock domains)
		{
			re:   regexp.MustCompile(`\b[a-zA-Z0-9_.+-]+@[a-zA-Z0-9-]+\.[a-zA-Z0-9-.]+\b`),
			repl: "[REDACTED_EMAIL]",
		},
	}
}

// Redact sanitizes an arbitrary text string, replacing discovered secrets,
// generic API tokens, phone numbers, and credentials.
func (r *Redactor) Redact(text string) string {
	if r == nil || text == "" {
		return text
	}

	result := text

	// 1. Exact discovered secrets & user-configured words
	for _, secret := range r.secrets {
		if strings.Contains(result, secret) {
			result = strings.ReplaceAll(result, secret, "[REDACTED_SECRET]")
		}
	}

	// 2. Generic regex patterns
	for _, p := range r.patterns {
		if p.repl == "[REDACTED_EMAIL]" {
			result = p.re.ReplaceAllStringFunc(result, func(m string) string {
				lower := strings.ToLower(m)
				if strings.HasSuffix(lower, "@example.com") ||
					strings.HasSuffix(lower, "@domain.com") ||
					strings.HasSuffix(lower, "@qdrant.tech") ||
					strings.HasSuffix(lower, "@github.com") {
					return m
				}
				return p.repl
			})
			continue
		}
		result = p.re.ReplaceAllString(result, p.repl)
	}

	return result
}
