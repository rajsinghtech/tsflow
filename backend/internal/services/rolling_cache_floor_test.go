package services

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

// After a restart the cursor can sit partway through a minute whose first
// part a previous process already wrote to the database. The new cache only
// sees the rest of that minute, so it must not answer for it.
func TestRollingCacheSkipsTheMinuteBeforeARestart(t *testing.T) {
	ctx := context.Background()
	minute := time.Now().Add(-5 * time.Minute).Truncate(time.Minute).UTC()
	cursor := minute.Add(30 * time.Second)

	logAt := func(at time.Time, bytes int) string {
		return fmt.Sprintf(`{"nodeId":"node-a","logged":%q,"start":%q,"virtualTraffic":[{"proto":6,"src":"100.64.0.1:1234","dst":"100.64.0.2:443","txBytes":%d,"txPkts":1}]}`,
			at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano), bytes)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"logs":[%s,%s]}`, logAt(minute.Add(40*time.Second), 100), logAt(minute.Add(90*time.Second), 200))
	}))
	defer server.Close()

	store, err := database.NewSQLiteStore(t.TempDir() + "/restart.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	// The previous process wrote the first half of the minute and stopped.
	if err := store.CommitPollResults(ctx, database.DefaultTailnetID, database.PollResults{
		NodePairs: []database.NodePairAggregate{{
			Bucket: minute.Unix(), SrcNodeID: "node-a", DstNodeID: "node-b", TrafficType: "virtual",
			TxBytes: 1000, TxPkts: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":1000}`, Ports: "[]",
		}},
		Bandwidth: []database.BandwidthBucket{{Time: minute, TxBytes: 1000}},
		PollEnd:   cursor,
	}); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultPollerConfig()
	cfg.PollDelay = 0
	poller := NewPoller(NewTailscaleService(&config.Config{TailscaleAPIURL: server.URL, TailscaleTailnet: "example.com"}), store, cfg)
	if err := poller.poll(ctx); err != nil {
		t.Fatal(err)
	}
	cache := poller.GetRollingCache()
	if len(cache.GetBandwidth(minute, minute.Add(time.Minute))) != 1 {
		t.Fatal("fixture: the new process should have cached part of the restart minute")
	}

	window := [2]time.Time{minute, minute.Add(2 * time.Minute)}
	if cache.HasNodePairDataFor(window[0], window[1]) || cache.HasBandwidthDataFor(window[0], window[1]) ||
		cache.HasTrafficStatsDataFor(window[0], window[1]) || cache.HasNodeBandwidthDataFor(window[0], window[1], "node-a") {
		t.Fatal("cache claims the restart minute it only saw half of")
	}
	after := [2]time.Time{minute.Add(time.Minute), minute.Add(2 * time.Minute)}
	if !cache.HasNodePairDataFor(after[0], after[1]) || !cache.HasBandwidthDataFor(after[0], after[1]) {
		t.Fatal("cache should still answer for minutes after the cursor")
	}
}

func TestRollingCacheCoverFromRoundsUpAndKeepsTheFirstFloor(t *testing.T) {
	now := time.Now().Truncate(time.Minute)
	buckets := map[int64]struct{}{}
	for m := -10; m <= 0; m++ {
		buckets[now.Add(time.Duration(m)*time.Minute).Unix()] = struct{}{}
	}
	c := NewRollingWindowCache(time.Hour)
	for b := range buckets {
		c.Update(nil, []database.BandwidthBucket{{Time: time.Unix(b, 0), TxBytes: 1}}, nil, nil)
	}
	if !c.HasBandwidthDataFor(now.Add(-10*time.Minute), now) {
		t.Fatal("no floor: the cache should cover its buckets")
	}
	c.CoverFrom(now.Add(-5*time.Minute + time.Second))
	c.CoverFrom(now.Add(-9 * time.Minute)) // ignored
	if c.HasBandwidthDataFor(now.Add(-5*time.Minute), now) {
		t.Fatal("the bucket holding the floor time must not be covered")
	}
	if !c.HasBandwidthDataFor(now.Add(-4*time.Minute), now) {
		t.Fatal("whole minutes after the floor should be covered")
	}
	aligned := NewRollingWindowCache(time.Hour)
	for b := range buckets {
		aligned.Update(nil, []database.BandwidthBucket{{Time: time.Unix(b, 0), TxBytes: 1}}, nil, nil)
	}
	aligned.CoverFrom(now.Add(-5 * time.Minute))
	if !aligned.HasBandwidthDataFor(now.Add(-5*time.Minute), now) {
		t.Fatal("a minute-aligned floor starts a whole minute and should be covered")
	}
}
