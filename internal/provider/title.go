package provider

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// TitleMaxLength bounds a derived title. It is measured in runes, not bytes:
// truncating a multi-byte character in half emits U+FFFD.
const TitleMaxLength = 80

// titleMarkdownPrefix matches the markdown decoration at the start of a line —
// heading hashes, list bullets, rules, quote markers.
var titleMarkdownPrefix = regexp.MustCompile(`^[#*\-=>\s]+`)

// CleanTitle derives a conversation title from its opening message.
//
// Two versions of this existed. Antigravity's unescaped literal \n and \t left
// by serialisation, stripped markdown decoration and fell back to the first
// line; Claude's did none of that, so a title made entirely of markdown
// punctuation came back empty. This is Antigravity's, made rune-safe, and both
// providers now use it.
func CleanTitle(text string) string {
	if text == "" {
		return ""
	}
	// Serialised transcripts carry these as two characters, not as the escape
	// they represent.
	for _, pair := range [][2]string{
		{`\r\n`, "\n"},
		{`\n`, "\n"},
		{`\t`, " "},
		{`\"`, `"`},
	} {
		text = strings.ReplaceAll(text, pair[0], pair[1])
	}

	lines := strings.Split(text, "\n")
	var candidate string
	for _, line := range lines {
		cleaned := strings.TrimSpace(titleMarkdownPrefix.ReplaceAllString(strings.TrimSpace(line), ""))
		// A workspace URI is the path, not a title.
		if cleaned != "" && !strings.HasPrefix(cleaned, "file://") {
			candidate = cleaned
			break
		}
	}
	// Nothing survived the stripping, so keep the first line's text rather than
	// returning no title at all.
	if candidate == "" && len(lines) > 0 {
		candidate = strings.TrimSpace(titleMarkdownPrefix.ReplaceAllString(strings.TrimSpace(lines[0]), ""))
	}
	return TruncateRunes(strings.TrimSpace(candidate), TitleMaxLength)
}

// TruncateRunes shortens s to at most limit runes. Cutting on a byte index
// splits a multi-byte character and produces a replacement character in the
// output, which is why every truncation here counts runes.
func TruncateRunes(s string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	count := 0
	for index := range s {
		if count == limit {
			return s[:index]
		}
		count++
	}
	return s
}
