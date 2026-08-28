package notify

import (
	"strings"
	"testing"
)

func TestSplitMessageKeepsShortBodyIntact(t *testing.T) {
	got := splitMessage("one\ntwo\n", 2000)

	if len(got) != 1 || got[0] != "one\ntwo" {
		t.Errorf("splitMessage = %q, want a single trimmed part", got)
	}
}

func TestSplitMessageBreaksUnbrokenLine(t *testing.T) {
	got := splitMessage(strings.Repeat("x", 25), 10)

	if len(got) != 3 {
		t.Fatalf("got %d parts, want 3", len(got))
	}
	if joined := strings.Join(got, ""); joined != strings.Repeat("x", 25) {
		t.Errorf("content lost: %q", joined)
	}
}
