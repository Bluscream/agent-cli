package antigravity

import (
	"bytes"
	"testing"
)

func TestVarintEncodeDecode(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 300, 16384, 1000000}
	for _, c := range cases {
		enc := encodeVarint(c)
		fields := parseMsg(append([]byte{0x08}, enc...)) // tag: field 1, wireType 0
		if len(fields) != 1 {
			t.Fatalf("expected 1 field for val %d, got %d", c, len(fields))
		}
		if fields[0].varint != c {
			t.Errorf("expected varint %d, got %d", c, fields[0].varint)
		}
	}
}

func TestBuildSummaryEntry(t *testing.T) {
	meta := ConvoMeta{
		ID:           "12345678-1234-1234-1234-123456789abc",
		Title:        "Test Conversation",
		StepCount:    5,
		CreatedTS:    1700000000,
		ModifiedTS:   1700001000,
		TrajectoryID: "12345678-1234-1234-1234-123456789abc",
		WorkspaceURI: "file:///workspace",
	}

	entryBytes, err := buildSummaryEntry(meta)
	if err != nil {
		t.Fatalf("buildSummaryEntry failed: %v", err)
	}

	if len(entryBytes) == 0 {
		t.Fatal("expected non-empty summary entry bytes")
	}

	// Verify that extractMapEntries parses our entry back
	entries, err := extractMapEntries(entryBytes)
	if err != nil {
		t.Fatalf("extractMapEntries failed: %v", err)
	}
	if _, ok := entries[meta.ID]; !ok {
		t.Errorf("expected conversation %s in map, got %v", meta.ID, entries)
	}
}

func TestCleanTitleText(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"# Hello world\nSome other text", "Hello world"},
		{"* file:///some/path\nSecond line", "Second line"},
		{"  -- fix the audio settings", "fix the audio settings"},
	}

	for _, tt := range tests {
		res := cleanTitleText(tt.input)
		if !bytes.Equal([]byte(res), []byte(tt.expect)) {
			t.Errorf("cleanTitleText(%q) = %q, expected %q", tt.input, res, tt.expect)
		}
	}
}
