package mcpserver

import (
	"fmt"
	"strings"
	"time"

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
	} else if end, err = time.Parse(time.RFC3339, strings.TrimSpace(endRaw)); err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid end time")
	}
	if end.After(now) {
		end = now
	}
	if strings.TrimSpace(startRaw) == "" {
		start = end.Add(-defaultWindow)
	} else if start, err = time.Parse(time.RFC3339, strings.TrimSpace(startRaw)); err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid start time")
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
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("lookback must be a positive duration such as 24h")
	}
	if d > maxLookback {
		return 0, fmt.Errorf("lookback cannot exceed 168h")
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
