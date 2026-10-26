package mcpserver

import (
	"strings"
	"testing"
)

// Each part of a combined duration was capped, but the sum was not, so
// enough parts wrapped time.Duration around into a small positive value.
func TestParseDurationRejectsOverflowingSum(t *testing.T) {
	raw := strings.Repeat("36500d", 5) + "31005.0749d"
	if d, err := parseDuration(raw); err == nil {
		t.Fatalf("parseDuration(%q) = %s, want an error", raw, d)
	}
	if d, err := parseLookback(raw); err == nil {
		t.Fatalf("parseLookback(%q) = %s, want an error", raw, d)
	}
	if d, err := parseDuration("1d12h"); err != nil || d.Hours() != 36 {
		t.Fatalf("parseDuration(1d12h) = %s, %v", d, err)
	}
}
