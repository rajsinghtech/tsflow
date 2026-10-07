package database

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestGraphReadUnderConcurrentWrites runs graph reads against one writer
// that polls (moving the rollup mark), writes late rows into closed hours
// through every write path, applies retention, and writes to a second
// tailnet. After each write the writer records the plain minute scan of
// every window. Each read must equal one recorded state between the last
// write that finished before it started and the first that finished after
// it ended. Cache sizes cover a warm cache, constant eviction and no cache.
func TestGraphReadUnderConcurrentWrites(t *testing.T) {
	cases := []struct {
		name  string
		cache int64
	}{
		{"cache", 64 << 20},
		{"tiny cache", 48 << 10},
		{"no cache", 0},
	}
	if raceEnabled {
		// The race detector makes every scan slow; the evicting cache
		// alone covers the shared state.
		cases = cases[1:2]
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runGraphReadStress(t, tc.cache)
		})
	}
}

type stressKey struct {
	tailnet string
	window  int
}

func runGraphReadStress(t *testing.T, cacheBytes int64) {
	prev := maxClosedHourReaders
	maxClosedHourReaders = 4
	t.Cleanup(func() { maxClosedHourReaders = prev })
	ctx := context.Background()
	store, mark := buildManyHourStore(t)
	store.SetClosedHourCacheBytes(cacheBytes)

	steps, readers, pause := 36, 4, time.Duration(0)
	if raceEnabled {
		steps, readers, pause = 8, 2, 5*time.Millisecond
	}
	end := mark + int64(2*steps+12)*minuteSeconds
	windows := [][2]int64{
		{manyHourBase + 6*hourSeconds, end},                  // live: closed hours, filling hour, new minutes
		{manyHourBase + 20*hourSeconds + 13*60, mark + 9*60}, // ragged, through the mark
		{manyHourBase + 2*hourSeconds, manyHourBase + 12*hourSeconds},
		{0, end + hourSeconds}, // from the epoch
	}
	if raceEnabled {
		windows = windows[1:2] // the ragged window that crosses the mark
	}
	tailnets := []string{DefaultTailnetID, "other"}
	snapshot := func() map[stressKey][]NodePairAggregate {
		out := map[stressKey][]NodePairAggregate{}
		for _, tn := range tailnets {
			for i, w := range windows {
				rows, err := store.legacyNodePairAggregates(ctx, tn, time.Unix(w[0], 0).UTC(), time.Unix(w[1], 0).UTC())
				if err != nil {
					t.Error(err)
				}
				out[stressKey{tn, i}] = rows
			}
		}
		return out
	}

	var mu sync.Mutex
	states := []map[stressKey][]NodePairAggregate{snapshot()}
	var version atomic.Int64
	var done, stop atomic.Bool
	writerExited := make(chan struct{})

	pair := func(bucket int64, src, dst string, tx int64, port int) NodePairAggregate {
		b := strconv.FormatInt(tx, 10)
		return NodePairAggregate{
			Bucket: bucket, SrcNodeID: src, DstNodeID: dst, TrafficType: "virtual",
			TxBytes: tx, RxBytes: tx / 3, TxPkts: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":` + b + `}`,
			Ports: fmt.Sprintf(`[{"port":%d,"proto":6,"bytes":%d}]`, port, tx), TxPorts: "[]", RxPorts: "[]",
			TxProtocolBytes: "{}", RxProtocolBytes: "{}",
		}
	}

	writerErr := make(chan error, 1)
	go func() {
		defer close(writerExited)
		defer done.Store(true)
		rng := rand.New(rand.NewSource(3))
		pollEnd := map[string]int64{DefaultTailnetID: mark + 2*minuteSeconds, "other": mark + 2*minuteSeconds}
		retentionCut := manyHourBase + hourSeconds
		for step := 1; step <= steps && !stop.Load(); step++ {
			tn := tailnets[rng.Intn(2)]
			var err error
			switch op := rng.Intn(6); op {
			case 0, 1: // a poll: new minutes after the mark, and the mark follows the poll end
				pollEnd[tn] += 2 * minuteSeconds
				err = store.CommitPollResults(ctx, tn, PollResults{
					NodePairs: []NodePairAggregate{
						pair(pollEnd[tn]-minuteSeconds, "tag:app", "tag:db", int64(100+step), 5432),
						pair(pollEnd[tn]-2*minuteSeconds, "poll-"+strconv.Itoa(step%5), "peer", int64(7+step), 443),
					},
					PollEnd: time.Unix(pollEnd[tn], 0).UTC(),
				})
			case 2: // a late poll row into a closed hour
				hour := manyHourBase + int64(2+rng.Intn(25))*hourSeconds
				err = store.CommitPollResults(ctx, tn, PollResults{NodePairs: []NodePairAggregate{
					pair(hour+int64(rng.Intn(60))*minuteSeconds, "tag:app", "late-"+strconv.Itoa(step%3), int64(11+step), 8443),
				}})
			case 3: // a late object
				hour := manyHourBase + int64(2+rng.Intn(25))*hourSeconds
				err = store.CommitObjectIngest(ctx, tn, ObjectIngestResult{
					Key: fmt.Sprintf("late-%s-%d", tn, step), LastModified: time.Unix(hour, 0),
					NodePairs: []NodePairAggregate{pair(hour+7*minuteSeconds, "a|b", "c|d", int64(13+step), 53)},
				})
			case 4: // a direct upsert into a closed hour
				hour := manyHourBase + int64(2+rng.Intn(25))*hourSeconds
				err = store.UpsertNodePairAggregates(ctx, tn, []NodePairAggregate{
					pair(hour+31*minuteSeconds, "tie-src", "tie-dst", int64(17+step), 22),
				})
			case 5: // retention moves forward, through the middle of an hour
				retentionCut += 37 * minuteSeconds
				_, err = store.Cleanup(ctx, tn, time.Since(time.Unix(retentionCut, 0)))
			}
			if err != nil {
				writerErr <- fmt.Errorf("step %d: %w", step, err)
				return
			}
			st := snapshot()
			mu.Lock()
			states = append(states, st)
			mu.Unlock()
			version.Store(int64(step))
		}
	}()

	var reads atomic.Int64
	var wg sync.WaitGroup
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(100 + r)))
			for !done.Load() || reads.Load() < 8 {
				time.Sleep(pause)
				key := stressKey{tailnets[rng.Intn(2)], rng.Intn(len(windows))}
				w := windows[key.window]
				v0 := version.Load()
				got, err := store.GetNodePairAggregates(ctx, key.tailnet, time.Unix(w[0], 0).UTC(), time.Unix(w[1], 0).UTC())
				v1 := version.Load()
				if err != nil {
					stop.Store(true)
					t.Errorf("read %v: %v", key, err)
					return
				}
				reads.Add(1)
				// The write after v1 may have committed before the reader saw
				// the version move, so it is a candidate too.
				hi := v1 + 1
				matched := false
				for v := v0; v <= hi && !matched; v++ {
					var st map[stressKey][]NodePairAggregate
					for st == nil {
						mu.Lock()
						if int(v) < len(states) {
							st = states[v]
						}
						mu.Unlock()
						if st == nil {
							if done.Load() {
								break
							}
							time.Sleep(time.Millisecond)
						}
					}
					if st != nil && reflect.DeepEqual(st[key], got) {
						matched = true
					}
				}
				if !matched {
					stop.Store(true)
					mu.Lock()
					want := states[v0][key]
					mu.Unlock()
					t.Errorf("read %v between versions %d and %d matched no recorded state\nat %d: %s\ngot:  %s",
						key, v0, v1, v0, pairRowsJSON(want), pairRowsJSON(got))
					return
				}
			}
		}(r)
	}
	wg.Wait()
	<-writerExited
	if t.Failed() {
		return
	}
	select {
	case err := <-writerErr:
		t.Fatal(err)
	default:
	}
	if version.Load() != int64(steps) {
		t.Fatalf("writer stopped at step %d", version.Load())
	}
	t.Logf("%d reads over %d writes, %d fallbacks, cache %+v", reads.Load(), steps, store.openReadFallbacks.Load(), store.ClosedHourCacheStats())
	// Quiet reads at the end match the final state exactly.
	final := snapshot()
	for key, want := range final {
		w := windows[key.window]
		got, err := store.GetNodePairAggregates(ctx, key.tailnet, time.Unix(w[0], 0).UTC(), time.Unix(w[1], 0).UTC())
		if err != nil || !reflect.DeepEqual(want, got) {
			t.Fatalf("final read %v err=%v\nwant %s\ngot  %s", key, err, pairRowsJSON(want), pairRowsJSON(got))
		}
	}
}
