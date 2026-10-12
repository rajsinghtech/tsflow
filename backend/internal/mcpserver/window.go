package mcpserver

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
)

const (
	defaultWindow      = time.Hour
	maxWindow          = 7 * 24 * time.Hour
	defaultLimit       = 20
	maxLimit           = 100
	maxOffset          = 1000
	defaultLookback    = 24 * time.Hour
	maxLookback        = 7 * 24 * time.Hour
	maxScanRows        = 5000
	maxNewPairs        = 100000
	maxEndpointIDs     = 64
	maxTimelineBuckets = 500
	maxPorts           = 20
)

func parseWindow(startRaw, endRaw string) (time.Time, time.Time, error) {
	return parseWindowAt(startRaw, endRaw, time.Now().UTC())
}

// parseWindowAt resolves a window against now. An end in the future is
// clamped to now before the default start is derived, so "end only" still
// means the hour before it. A start in the future is reported as such
// instead of as an empty or inverted window.
func parseWindowAt(startRaw, endRaw string, now time.Time) (time.Time, time.Time, error) {
	var (
		start time.Time
		end   time.Time
		err   error
	)
	now = now.UTC()
	if strings.TrimSpace(endRaw) == "" {
		end = now
	} else if end, err = parseTime(endRaw, now); err != nil {
		return time.Time{}, time.Time{}, timeError("end", endRaw)
	}
	if end.After(now) {
		end = now
	}
	if strings.TrimSpace(startRaw) == "" {
		start = end.Add(-defaultWindow)
	} else if start, err = parseTime(startRaw, now); err != nil {
		return time.Time{}, time.Time{}, timeError("start", startRaw)
	}
	if !start.Before(now) {
		return time.Time{}, time.Time{}, fmt.Errorf("start time %s is in the future (server time is %s)",
			start.UTC().Format(time.RFC3339), now.Format(time.RFC3339))
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("end time must be after start time")
	}
	if end.Sub(start) < time.Second {
		return time.Time{}, time.Time{}, fmt.Errorf("time range too small, minimum is 1s")
	}
	if end.Sub(start) > maxWindow {
		return time.Time{}, time.Time{}, fmt.Errorf("time range too large, maximum is 7 days")
	}
	return start, end, nil
}

// timeFormats are the absolute forms parseTime accepts. A time without a zone
// is read as UTC.
var timeFormats = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
}

// parseTime reads an RFC3339 time, a date or zone-less time in UTC, "now",
// or a time relative to now. Relative times are a duration back from now,
// written as now-2h, -2h, 2h, 2h ago, last 2h, or 2 hours ago. Durations take
// s, m, h, d, and w units and can be combined, as in 1d12h.
func parseTime(raw string, now time.Time) (time.Time, error) {
	text := strings.TrimSpace(raw)
	for _, layout := range timeFormats {
		if t, err := time.Parse(layout, text); err == nil {
			return t.UTC(), nil
		}
	}
	lower := strings.ToLower(text)
	if lower == "now" {
		return now, nil
	}
	rel := lower
	if after, ok := strings.CutPrefix(rel, "now"); ok {
		after = strings.TrimSpace(after)
		if !strings.HasPrefix(after, "-") {
			return time.Time{}, fmt.Errorf("invalid time")
		}
		rel = after
	}
	rel = strings.TrimSpace(strings.TrimPrefix(rel, "-"))
	rel = strings.TrimSpace(strings.TrimPrefix(rel, "last "))
	rel = strings.TrimSpace(strings.TrimSuffix(rel, " ago"))
	if unit, ok := durationUnits[rel]; ok && len(rel) > 1 {
		// "last hour", "last day", "last week".
		return now.Add(-unit), nil
	}
	d, err := parseDuration(rel)
	if err != nil {
		return time.Time{}, err
	}
	return now.Add(-d), nil
}

func timeError(field, raw string) error {
	return fmt.Errorf("invalid %s time %q: use RFC3339 such as 2026-01-02T15:04:05Z, or a time relative to now such as now-2h, 2h ago, or last 7d", field, strings.TrimSpace(raw))
}

// maxParsedDuration keeps a parsed duration far from overflowing. Range
// checks such as the 7 day window apply after parsing.
const maxParsedDuration = 100 * 365 * 24 * time.Hour

// durationUnits maps the unit words parseDuration accepts to their length.
var durationUnits = map[string]time.Duration{
	"s": time.Second, "sec": time.Second, "secs": time.Second, "second": time.Second, "seconds": time.Second,
	"m": time.Minute, "min": time.Minute, "mins": time.Minute, "minute": time.Minute, "minutes": time.Minute,
	"h": time.Hour, "hr": time.Hour, "hrs": time.Hour, "hour": time.Hour, "hours": time.Hour,
	"d": 24 * time.Hour, "day": 24 * time.Hour, "days": 24 * time.Hour,
	"w": 7 * 24 * time.Hour, "wk": 7 * 24 * time.Hour, "week": 7 * 24 * time.Hour, "weeks": 7 * 24 * time.Hour,
}

// parseDuration reads a positive duration made of number and unit pairs,
// such as 90m, 1d12h, 1.5h, or 2 hours. It also reads anything
// time.ParseDuration does, so existing values such as 24h keep working.
func parseDuration(raw string) (time.Duration, error) {
	text := strings.TrimSpace(strings.ToLower(raw))
	if d, err := time.ParseDuration(text); err == nil {
		if d <= 0 {
			return 0, fmt.Errorf("duration must be positive")
		}
		return d, nil
	}
	var total time.Duration
	rest := text
	for rest != "" {
		i := 0
		for i < len(rest) && (rest[i] >= '0' && rest[i] <= '9' || rest[i] == '.') {
			i++
		}
		if i == 0 {
			return 0, fmt.Errorf("invalid duration")
		}
		n, err := strconv.ParseFloat(rest[:i], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration")
		}
		rest = strings.TrimLeft(rest[i:], " ")
		j := 0
		for j < len(rest) && unicode.IsLetter(rune(rest[j])) {
			j++
		}
		unit, ok := durationUnits[rest[:j]]
		if !ok {
			return 0, fmt.Errorf("invalid duration")
		}
		if n > float64(maxParsedDuration/unit) {
			return 0, fmt.Errorf("duration too large")
		}
		total += time.Duration(n * float64(unit))
		rest = strings.TrimLeft(rest[j:], " ")
	}
	if total <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	return total, nil
}

func parsePage(limit, offset int) (int, int, error) {
	if limit == 0 {
		limit = defaultLimit
	}
	if limit < 0 {
		return 0, 0, fmt.Errorf("limit must be positive")
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if offset < 0 || offset > maxOffset {
		return 0, 0, fmt.Errorf("offset must be a non-negative integer no larger than %d", maxOffset)
	}
	return limit, offset, nil
}

func parseLookback(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultLookback, nil
	}
	d, err := parseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("lookback %q must be a positive duration such as 24h, 3d, or 1w", raw)
	}
	if d > maxLookback {
		return 0, fmt.Errorf("lookback cannot exceed 7d (168h)")
	}
	return d, nil
}

func parseTypes(values []string) ([]string, error) {
	return handlers.ParseTrafficTypes(values)
}

func page[T any](items []T, offset, limit int) ([]T, bool) {
	if offset >= len(items) {
		if items == nil {
			return []T{}, false
		}
		return []T{}, false
	}
	end := offset + limit
	more := end < len(items)
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end], more
}
