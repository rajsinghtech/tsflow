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

func TestParseWindowRelativeTimes(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"now-2h":        2 * time.Hour,
		"now - 2h":      2 * time.Hour,
		"NOW-90m":       90 * time.Minute,
		"-2h":           2 * time.Hour,
		"2h":            2 * time.Hour,
		"2h ago":        2 * time.Hour,
		"2 hours ago":   2 * time.Hour,
		"last 2h":       2 * time.Hour,
		"last 30 mins":  30 * time.Minute,
		"last hour":     time.Hour,
		"last day":      24 * time.Hour,
		"last week":     7 * 24 * time.Hour,
		"1d":            24 * time.Hour,
		"now-1d12h":     36 * time.Hour,
		"1.5h":          90 * time.Minute,
		"1w":            7 * 24 * time.Hour,
		" last 7 days ": 7 * 24 * time.Hour,
	}
	for raw, back := range cases {
		start, end, err := parseWindowAt(raw, "", now)
		if err != nil {
			t.Errorf("start %q: %v", raw, err)
			continue
		}
		if !start.Equal(now.Add(-back)) || !end.Equal(now) {
			t.Errorf("start %q = %s..%s, want %s back from now", raw, start, end, back)
		}
	}

	start, end, err := parseWindowAt("now-3h", "now-1h", now)
	if err != nil || !start.Equal(now.Add(-3*time.Hour)) || !end.Equal(now.Add(-time.Hour)) {
		t.Fatalf("relative end = %s..%s err=%v", start, end, err)
	}
	start, end, err = parseWindowAt("", "now", now)
	if err != nil || !end.Equal(now) || !start.Equal(now.Add(-defaultWindow)) {
		t.Fatalf("end=now = %s..%s err=%v", start, end, err)
	}
}

func TestParseWindowAbsoluteForms(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Time{
		"2026-05-08T10:00:00Z":      time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC),
		"2026-05-08T05:00:00-05:00": time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC),
		"2026-05-08T10:00:00.5Z":    time.Date(2026, 5, 8, 10, 0, 0, 5e8, time.UTC),
		"2026-05-08T10:00:00":       time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC),
		"2026-05-08 10:00":          time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC),
		"2026-05-08":                time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC),
	}
	for raw, want := range cases {
		start, _, err := parseWindowAt(raw, "", now)
		if err != nil || !start.Equal(want) {
			t.Errorf("start %q = %s err=%v, want %s", raw, start, err, want)
		}
	}
}

func TestParseWindowBadTimesExplainTheForms(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	for _, raw := range []string{"yesterday", "nowish", "now+1h", "2 fortnights", "last", "0h", "-0s", "h", "1e9999h"} {
		_, _, err := parseWindowAt(raw, "", now)
		if err == nil {
			t.Errorf("start %q parsed", raw)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "invalid start time") || !strings.Contains(msg, "RFC3339") || !strings.Contains(msg, "now-2h") {
			t.Errorf("start %q error = %q", raw, msg)
		}
	}
	// A relative start past the 7 day cap reports the cap, not a parse error.
	if _, _, err := parseWindowAt("30d", "", now); err == nil || !strings.Contains(err.Error(), "maximum is 7 days") {
		t.Fatalf("30d error = %v", err)
	}
}

func TestParseLookbackUnits(t *testing.T) {
	cases := map[string]time.Duration{
		"":        defaultLookback,
		"24h":     24 * time.Hour,
		"90m":     90 * time.Minute,
		"3d":      3 * 24 * time.Hour,
		"1w":      7 * 24 * time.Hour,
		"2 days":  2 * 24 * time.Hour,
		"1d12h":   36 * time.Hour,
		"168h":    maxLookback,
		"1h30m0s": 90 * time.Minute,
	}
	for raw, want := range cases {
		got, err := parseLookback(raw)
		if err != nil || got != want {
			t.Errorf("parseLookback(%q) = %s, %v; want %s", raw, got, err, want)
		}
	}
	for raw, want := range map[string]string{"8d": "cannot exceed 7d", "2w": "cannot exceed 7d", "-1h": "positive duration", "soon": "positive duration"} {
		if _, err := parseLookback(raw); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("parseLookback(%q) = %v, want %q", raw, err, want)
		}
	}
}
