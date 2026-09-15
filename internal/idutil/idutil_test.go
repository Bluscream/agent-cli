package idutil

import "testing"

func TestShortID(t *testing.T) {
	id1 := "98256169-57f7-4239-9eaf-c73532e3253d"
	s1 := ShortID(id1)
	if len(s1) != 8 {
		t.Fatalf("expected 8 chars, got %d (%s)", len(s1), s1)
	}

	// Deterministic
	if ShortID(id1) != s1 {
		t.Fatalf("expected deterministic output")
	}

	// Different raw IDs give different short IDs
	id2 := "project--run-media-system-Data-Projects-pipemeeter-prefer-existing-cr"
	s2 := ShortID(id2)
	if s1 == s2 {
		t.Fatalf("unexpected collision between distinct IDs")
	}
}

func TestMatch(t *testing.T) {
	raw := "98256169-57f7-4239-9eaf-c73532e3253d"
	short := ShortID(raw)

	if !Match(raw, raw) {
		t.Errorf("exact match failed")
	}
	if !Match(short, raw) {
		t.Errorf("short match failed")
	}
	if !Match("98256169", raw) {
		t.Errorf("raw prefix match failed")
	}
	if !Match(short[:5], raw) {
		t.Errorf("short prefix match failed")
	}
	if Match("notanid", raw) {
		t.Errorf("false positive match")
	}
}
