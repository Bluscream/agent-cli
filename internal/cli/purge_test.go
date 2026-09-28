package cli

import (
	"strings"
	"testing"
)

// A question that erases transcripts must not treat a bare Return as consent.
func TestConfirmDestructiveRequiresATypedYes(t *testing.T) {
	for _, answer := range []string{"\n", "y\n", "Y\n", "sure\n", "", "no\n", "YES please\n"} {
		var out strings.Builder
		ok, err := confirmDestructive(strings.NewReader(answer), &out, "everything", false)
		if err != nil {
			t.Fatalf("answer %q: %v", answer, err)
		}
		if ok {
			t.Fatalf("answer %q was accepted as confirmation", answer)
		}
	}
}

func TestConfirmDestructiveAcceptsYes(t *testing.T) {
	for _, answer := range []string{"yes\n", "YES\n", " yes \n", "Yes"} {
		var out strings.Builder
		ok, err := confirmDestructive(strings.NewReader(answer), &out, "everything", false)
		if err != nil {
			t.Fatalf("answer %q: %v", answer, err)
		}
		if !ok {
			t.Fatalf("answer %q was not accepted", answer)
		}
	}
}

// The prompt has to say what is about to go, including that it reaches the
// collection, so consent is informed rather than generic.
func TestConfirmDestructiveNamesTheScope(t *testing.T) {
	var out strings.Builder
	if _, err := confirmDestructive(strings.NewReader("no\n"), &out, "every conversation of codex", true); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"every conversation of codex", "ingestion collection", "cannot be undone"} {
		if !strings.Contains(text, want) {
			t.Fatalf("prompt does not mention %q:\n%s", want, text)
		}
	}
}
