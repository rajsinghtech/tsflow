package database

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestSlowIngestDoesNotBlockOtherTailnet(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const bucket int64 = 1_700_000_040
	if err := store.CommitPollResults(ctx, "beta", pollWithPair(bucket, 42, "src", "dst")); err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	defer letGo()

	entered := make(chan struct{})
	var once sync.Once
	store.writeStarted = func(id string) {
		if id != "alpha" {
			return
		}
		once.Do(func() { close(entered) })
		<-release
	}
	defer func() { store.writeStarted = nil }()

	ingestDone := make(chan error, 1)
	go func() {
		ingestDone <- store.CommitPollResults(ctx, "alpha", pollWithPair(bucket, 999, "src", "dst"))
	}()
	select {
	case <-entered:
	case err := <-ingestDone:
		t.Fatalf("ingest finished before it was inside the write: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("ingest did not start")
	}

	queryDone := make(chan error, 1)
	go func() {
		queryDone <- readTailnetSnapshot(ctx, store, "beta", bucket, 42, 3)
	}()
	select {
	case err := <-queryDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("query on beta blocked while alpha ingest was in progress")
	}
	letGo()
	if err := <-ingestDone; err != nil {
		t.Fatal(err)
	}
}

func TestReadDuringIngestSeesLastCommit(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const bucket int64 = 1_700_000_040
	const tailnet = "alpha"
	if err := store.CommitPollResults(ctx, tailnet, pollWithPair(bucket, 10, "src", "dst")); err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	defer letGo()

	entered := make(chan struct{})
	var once sync.Once
	store.beforeCommit = func(id string) {
		if id != tailnet {
			return
		}
		once.Do(func() { close(entered) })
		<-release
	}
	defer func() { store.beforeCommit = nil }()

	ingestDone := make(chan error, 1)
	go func() {
		// The second commit adds to the first. It must stay invisible until Commit.
		ingestDone <- store.CommitPollResults(ctx, tailnet, pollWithPair(bucket, 999, "src", "dst"))
	}()
	select {
	case <-entered:
	case err := <-ingestDone:
		t.Fatalf("ingest finished before the commit hook: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("ingest did not reach commit")
	}

	queryDone := make(chan error, 1)
	go func() {
		queryDone <- readTailnetSnapshot(ctx, store, tailnet, bucket, 10, 3)
	}()
	select {
	case err := <-queryDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read blocked during ingest on the same tailnet")
	}
	letGo()
	if err := <-ingestDone; err != nil {
		t.Fatal(err)
	}
	if err := readTailnetSnapshot(ctx, store, tailnet, bucket, 10+999, 6); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentCommitsOnTwoTailnets(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const bucket int64 = 1_700_000_040
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []string{"alpha", "beta"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.CommitPollResults(ctx, id, pollWithPair(bucket, 10, "src", "dst"))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"alpha", "beta"} {
		if err := readTailnetSnapshot(ctx, store, id, bucket, 10, 3); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentCommitsOnOneTailnetAddBytes(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const bucket int64 = 1_700_000_040
	adds := []int64{10, 7}
	errs := make([]error, len(adds))
	var wg sync.WaitGroup
	for i, add := range adds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = store.CommitPollResults(ctx, "alpha", pollWithPair(bucket, add, "src", "dst"))
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("commit %d: %v", i, err)
		}
	}
	if err := readTailnetSnapshot(ctx, store, "alpha", bucket, 17, 6); err != nil {
		t.Fatal(err)
	}
}

// readTailnetSnapshot checks the pair, bandwidth, and traffic-stat views of one
// committed poll. flowCount is the pair's flow count and the stats total.
func readTailnetSnapshot(ctx context.Context, store *SQLiteStore, tailnetID string, bucket, txBytes, flows int64) error {
	start := time.Unix(bucket, 0).UTC()
	end := start.Add(time.Minute)
	pairs, err := store.GetNodePairAggregates(ctx, tailnetID, start, end)
	if err != nil {
		return fmt.Errorf("pairs: %w", err)
	}
	if len(pairs) != 1 || pairs[0].TxBytes != txBytes || pairs[0].FlowCount != flows {
		return fmt.Errorf("pairs = %+v, want tx %d flows %d", pairs, txBytes, flows)
	}
	bandwidth, err := store.GetBandwidth(ctx, tailnetID, start, end)
	if err != nil {
		return fmt.Errorf("bandwidth: %w", err)
	}
	if len(bandwidth) != 1 || bandwidth[0].TxBytes != txBytes {
		return fmt.Errorf("bandwidth = %+v, want tx %d", bandwidth, txBytes)
	}
	stats, err := store.GetTrafficStats(ctx, tailnetID, start, end)
	if err != nil {
		return fmt.Errorf("stats: %w", err)
	}
	if len(stats) != 1 || stats[0].TCPBytes != txBytes || stats[0].TotalFlows != flows {
		return fmt.Errorf("stats = %+v, want tcp %d flows %d", stats, txBytes, flows)
	}
	return nil
}

func pollWithPair(bucket, txBytes int64, src, dst string) PollResults {
	return PollResults{
		PollEnd: time.Unix(bucket+60, 0).UTC(),
		NodePairs: []NodePairAggregate{{
			Bucket:           bucket,
			SrcNodeID:        src,
			DstNodeID:        dst,
			TrafficType:      "virtual",
			TxBytes:          txBytes,
			RxBytes:          2,
			TxPkts:           1,
			RxPkts:           1,
			FlowCount:        3,
			Protocols:        "[6]",
			ProtocolBytes:    fmt.Sprintf(`{"6":%d}`, txBytes),
			Ports:            fmt.Sprintf(`[{"port":443,"proto":6,"bytes":%d}]`, txBytes),
			TxPorts:          fmt.Sprintf(`[{"port":443,"proto":6,"bytes":%d}]`, txBytes),
			RxPorts:          "[]",
			TxProtocolBytes:  fmt.Sprintf(`{"6":%d}`, txBytes),
			RxProtocolBytes:  "{}",
			DirectionalPorts: true,
		}},
		Bandwidth: []BandwidthBucket{{
			Time:    time.Unix(bucket, 0).UTC(),
			TxBytes: txBytes,
			RxBytes: 2,
		}},
		TrafficStats: []TrafficStats{{
			Bucket:       bucket,
			TCPBytes:     txBytes,
			VirtualBytes: txBytes,
			TotalFlows:   3,
			UniquePairs:  1,
			TopPorts:     fmt.Sprintf(`[{"port":443,"proto":6,"bytes":%d}]`, txBytes),
		}},
	}
}
