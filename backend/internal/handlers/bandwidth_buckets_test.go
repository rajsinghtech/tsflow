package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

type bandwidthBody struct {
	Buckets []struct {
		Time    time.Time `json:"time"`
		TxBytes int64     `json:"txBytes"`
		Seconds int64     `json:"seconds"`
	} `json:"buckets"`
	Metadata struct {
		BucketSeconds int64 `json:"bucketSeconds"`
	} `json:"metadata"`
}

// A quiet tailnet has minutes with no traffic. The bucket width must still
// be the store's grouping width, not the gap between non-empty buckets, or
// the chart divides each minute's bytes by ten minutes.
func TestBandwidthBucketSecondsIgnoresGapsBetweenBuckets(t *testing.T) {
	store := setupHandlerTestDB(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-90 * time.Minute)
	ctx := context.Background()

	// Traffic in one minute out of every ten.
	var minutes []time.Time
	for i := 0; i < 6; i++ {
		minutes = append(minutes, base.Add(time.Duration(i*10)*time.Minute))
	}
	buckets := make([]database.BandwidthBucket, 0, len(minutes))
	nodeBuckets := make([]database.NodeBandwidth, 0, len(minutes))
	pairs := make([]database.NodePairAggregate, 0, len(minutes))
	for _, minute := range minutes {
		buckets = append(buckets, database.BandwidthBucket{Time: minute, TxBytes: 6000})
		nodeBuckets = append(nodeBuckets, database.NodeBandwidth{Bucket: minute.Unix(), NodeID: "node-a", TxBytes: 6000})
		pairs = append(pairs, database.NodePairAggregate{
			Bucket: minute.Unix(), SrcNodeID: "node-a", DstNodeID: "node-b", TrafficType: "virtual",
			TxBytes: 6000, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":6000}`,
		})
	}
	if err := store.UpsertBandwidth(ctx, database.DefaultTailnetID, buckets); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodeBandwidth(ctx, database.DefaultTailnetID, nodeBuckets); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodePairAggregates(ctx, database.DefaultTailnetID, pairs); err != nil {
		t.Fatal(err)
	}

	h := &Handlers{store: store}
	window := "start=" + base.Format(time.RFC3339) + "&end=" + base.Add(time.Hour).Format(time.RFC3339)
	for _, target := range []string{
		"/api/bandwidth?" + window,
		"/api/bandwidth?" + window + "&trafficTypes=virtual",
		"/api/bandwidth?" + window + "&nodeId=node-a",
	} {
		var body bandwidthBody
		raw := handlerJSON(t, h, func(c *gin.Context) { h.GetBandwidthAggregated(c) }, target)
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if body.Metadata.BucketSeconds != 60 {
			t.Fatalf("%s: bucketSeconds = %d, want 60 for a one-hour window", target, body.Metadata.BucketSeconds)
		}
		if len(body.Buckets) != 6 {
			t.Fatalf("%s: got %d buckets, want 6", target, len(body.Buckets))
		}
		for _, bucket := range body.Buckets {
			if bucket.Seconds != 60 {
				t.Fatalf("%s: bucket %s covers %ds, want 60", target, bucket.Time, bucket.Seconds)
			}
		}
	}
}

// Daily buckets on non-adjacent days are still one day wide.
func TestBandwidthBucketSecondsForSparseDailyBuckets(t *testing.T) {
	store := setupHandlerTestDB(t)
	day := time.Now().UTC().Truncate(24 * time.Hour)
	ctx := context.Background()
	if err := store.UpsertBandwidth(ctx, database.DefaultTailnetID, []database.BandwidthBucket{
		{Time: day.Add(-5 * 24 * time.Hour).Add(time.Hour), TxBytes: 100},
		{Time: day.Add(-2 * 24 * time.Hour).Add(time.Hour), TxBytes: 100},
	}); err != nil {
		t.Fatal(err)
	}
	h := &Handlers{store: store}
	target := "/api/bandwidth?start=" + day.Add(-6*24*time.Hour).Format(time.RFC3339) + "&end=" + day.Format(time.RFC3339)
	var body bandwidthBody
	if err := json.Unmarshal([]byte(handlerJSON(t, h, func(c *gin.Context) { h.GetBandwidthAggregated(c) }, target)), &body); err != nil {
		t.Fatal(err)
	}
	if body.Metadata.BucketSeconds != 86400 {
		t.Fatalf("bucketSeconds = %d, want 86400", body.Metadata.BucketSeconds)
	}
}
