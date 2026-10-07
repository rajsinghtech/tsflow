package mcpserver

import (
	"strings"
	"testing"
	"time"
)

func TestParseWindowFutureTimes(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)

	// A start in the future names the problem.
	_, _, err := parseWindowAt("2026-05-08T13:00:00Z", "2026-05-08T14:00:00Z", now)
	if err == nil || !strings.Contains(err.Error(), "in the future") {
		t.Fatalf("future start error = %v, want it to say the start is in the future", err)
	}
	_, _, err = parseWindowAt("2026-05-08T13:00:00Z", "", now)
	if err == nil || !strings.Contains(err.Error(), "in the future") {
		t.Fatalf("future start without end error = %v", err)
	}

	// End alone in the future is clamped first, so the default hour ends now.
	start, end, err := parseWindowAt("", "2026-05-08T15:00:00Z", now)
	if err != nil {
		t.Fatalf("future end only: %v", err)
	}
	if !end.Equal(now) || !start.Equal(now.Add(-time.Hour)) {
		t.Fatalf("future end only = %s..%s, want the hour before now", start, end)
	}

	// A past start with a future end is clamped to now.
	start, end, err = parseWindowAt("2026-05-08T11:30:00Z", "2026-05-09T00:00:00Z", now)
	if err != nil || !end.Equal(now) || !start.Equal(now.Add(-30*time.Minute)) {
		t.Fatalf("clamped window = %s..%s err=%v", start, end, err)
	}
}

func TestParseWindowExistingErrors(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	cases := map[string][2]string{
		"invalid start time":     {"yesterday", ""},
		"invalid end time":       {"", "soon"},
		"end time must be after": {"2026-05-08T11:00:00Z", "2026-05-08T10:00:00Z"},
		"time range too large":   {"2026-04-01T00:00:00Z", "2026-05-08T11:00:00Z"},
		"time range too small":   {"2026-05-08T11:00:00.5Z", "2026-05-08T11:00:01Z"},
	}
	for want, in := range cases {
		if _, _, err := parseWindowAt(in[0], in[1], now); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("parseWindowAt(%q, %q) = %v, want %q", in[0], in[1], err, want)
		}
	}
	start, end, err := parseWindowAt("", "", now)
	if err != nil || !end.Equal(now) || !start.Equal(now.Add(-defaultWindow)) {
		t.Fatalf("default window = %s..%s err=%v", start, end, err)
	}
}
