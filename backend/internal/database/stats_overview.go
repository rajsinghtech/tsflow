package database

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// FillMissingTrafficStats derives overview buckets for the part of [start, end)
// that primary does not already cover. primary is the aggregated traffic_stats
// result for that same window, including its unique-pair recount.
//
// A covered window returns nil and does not read node_pairs. Rows inside a
// covered aggregate stay on the traffic_stats values, which is what the
// overview merge already did after scanning every pair in the window.
func (s *SQLiteStore) FillMissingTrafficStats(ctx context.Context, tailnetID string, start, end time.Time, primary []TrafficStats) ([]TrafficStats, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}

	ranges := missingAggregatedRanges(primary, start, end)
	if len(ranges) == 0 {
		return nil, nil
	}

	return s.queryTrafficStatsFromNodePairs(ctx, tailnetID, start, end, ranges, nil)
}

// DerivedStatsScanCount reports how many times node_pairs was read to derive
// traffic stats. A covered overview does not increment it.
func (s *SQLiteStore) DerivedStatsScanCount() int64 {
	return s.derivedStatsScans.Load()
}

func (s *SQLiteStore) noteDerivedStatsRead(ranges [][2]int64) {
	s.derivedStatsScans.Add(1)
	if s.derivedStatsHook == nil {
		return
	}
	copied := make([][2]int64, len(ranges))
	copy(copied, ranges)
	s.derivedStatsHook(copied)
}

// missingAggregatedRanges returns the half-open intervals in [start, end)
// whose aggregated bucket is not in primary. Adjacent gaps are one interval.
// The bucket size is the full window's, matching GetTrafficStats.
func missingAggregatedRanges(primary []TrafficStats, start, end time.Time) [][2]int64 {
	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil
	}
	bs := resolveBucketSize(endUnix - startUnix)
	covered := make(map[int64]struct{}, len(primary))
	for _, bucket := range primary {
		covered[bucket.Bucket] = struct{}{}
	}

	var ranges [][2]int64
	first := (startUnix / bs) * bs
	last := ((endUnix - 1) / bs) * bs
	var (
		open      bool
		openStart int64
		openEnd   int64
	)
	for b := first; b <= last; {
		segStart := b
		if segStart < startUnix {
			segStart = startUnix
		}
		segEnd := b + bs
		if segEnd > endUnix {
			segEnd = endUnix
		}
		if segStart < segEnd {
			if _, ok := covered[b]; ok {
				if open {
					ranges = append(ranges, [2]int64{openStart, openEnd})
					open = false
				}
			} else if !open {
				open = true
				openStart = segStart
				openEnd = segEnd
			} else {
				openEnd = segEnd
			}
		}
		next := b + bs
		if next <= b {
			break
		}
		b = next
	}
	if open {
		ranges = append(ranges, [2]int64{openStart, openEnd})
	}
	return ranges
}

func bucketRangePredicate(column string, ranges [][2]int64) (string, []any) {
	parts := make([]string, len(ranges))
	args := make([]any, 0, len(ranges)*2)
	for i, r := range ranges {
		parts[i] = fmt.Sprintf("(%s >= ? AND %s < ?)", column, column)
		args = append(args, r[0], r[1])
	}
	if len(parts) == 1 {
		return parts[0], args
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}
