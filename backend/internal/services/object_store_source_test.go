package services

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"path"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	_ "modernc.org/sqlite"
)

func TestObjectTimeSupportedCompressionSuffixes(t *testing.T) {
	tests := []string{
		"network/2026/05/08/2026-05-08-13-45-00.ndjson",
		"network/2026/05/08/2026-05-08-13-45-00.ndjson.zst",
		"network/2026/05/08/2026-05-08-13-45-00.ndjson.zstd",
		"network/2026/05/08/2026-05-08-13-45-00.ndjson.gz",
		"network/2026/05/08/2026-05-08-13-45-00.ndjson.gzip",
	}
	expected := time.Date(2026, 5, 8, 13, 45, 0, 0, time.UTC)

	for _, key := range tests {
		t.Run(key, func(t *testing.T) {
			got, ok := objectTime(key)
			if !ok {
				t.Fatalf("expected %s to parse", key)
			}
			if !got.Equal(expected) {
				t.Fatalf("expected %s, got %s", expected, got)
			}
		})
	}
}

func TestObjectTimeRejectsUnknownSuffix(t *testing.T) {
	if got, ok := objectTime("network/2026/05/08/2026-05-08-13-45-00.json.br"); ok {
		t.Fatalf("expected unsupported suffix to be rejected, got %s", got)
	}
}

func TestNewObjectStoreSourceRejectsInvalidEndpoint(t *testing.T) {
	for _, endpoint := range []string{"object-store.test", "ftp://object-store.test"} {
		if _, err := NewObjectStoreSource(context.Background(), ObjectStoreConfig{
			Bucket:   "bucket",
			Endpoint: endpoint,
		}); err == nil {
			t.Fatalf("endpoint %q was accepted", endpoint)
		}
	}
}

type testObject struct {
	key  string
	body []byte
}

type objectStoreTestDB struct {
	store   *database.SQLiteStore
	dbPath  string
	cleanup func()
}

func newTestObjectStore(t *testing.T, objects []testObject, maxObjects int) (*ObjectStoreSource, *httptest.Server) {
	t.Helper()
	byKey := make(map[string][]byte, len(objects))
	for _, object := range objects {
		byKey[object.key] = object.body
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list-type") == "2" {
			keys := make([]string, 0, len(byKey))
			for key := range byKey {
				keys = append(keys, key)
			}
			sort.Sort(sort.StringSlice(keys))

			var response bytes.Buffer
			response.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
			response.WriteString(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
			response.WriteString(`<Name>bucket</Name><KeyCount>`)
			response.WriteString(strconv.Itoa(len(keys)))
			response.WriteString(`</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated>`)
			for _, key := range keys {
				response.WriteString(`<Contents><Key>`)
				response.WriteString(html.EscapeString(key))
				response.WriteString(`</Key><LastModified>2026-05-08T13:45:00Z</LastModified><ETag>"test"</ETag><Size>`)
				response.WriteString(strconv.Itoa(len(byKey[key])))
				response.WriteString(`</Size><StorageClass>STANDARD</StorageClass></Contents>`)
			}
			response.WriteString(`</ListBucketResult>`)
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write(response.Bytes())
			return
		}

		key := strings.TrimPrefix(path.Clean(r.URL.Path), "/bucket/")
		body, ok := byKey[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))

	awsConfig := aws.Config{
		Region:      "test",
		Credentials: credentials.NewStaticCredentialsProvider("access", "secret", ""),
		HTTPClient:  server.Client(),
	}
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(server.URL)
		options.UsePathStyle = true
	})
	source := &ObjectStoreSource{
		cfg: ObjectStoreConfig{
			Bucket:     "bucket",
			Prefix:     "network",
			Lookback:   time.Hour,
			MaxObjects: maxObjects,
		},
		client: client,
	}
	return source, server
}

func testFlowObject(t *testing.T, key, nodeID string, bytes int64, withMetadata bool) testObject {
	t.Helper()
	logMap := map[string]any{
		"nodeId": nodeID,
		"start":  "2026-05-08T13:45:00Z",
		"virtualTraffic": []any{map[string]any{
			"proto":   6,
			"src":     "100.64.0.1:1234",
			"dst":     "100.64.0.2:443",
			"txBytes": bytes,
			"txPkts":  1,
		}},
	}
	if withMetadata {
		logMap["srcNode"] = map[string]any{
			"nodeId":    "node-a",
			"name":      "alpha.example.ts.net",
			"addresses": []string{"100.64.0.1"},
			"user":      "alice@example.com",
			"tags":      []string{"tag:prod"},
		}
		logMap["dstNodes"] = []any{map[string]any{
			"nodeId":    "node-b",
			"name":      "beta.example.ts.net",
			"addresses": []string{"100.64.0.2"},
		}}
	}
	body, err := json.Marshal(logMap)
	if err != nil {
		t.Fatal(err)
	}
	return testObject{key: key, body: append(body, '\n')}
}

func newObjectStoreTestPoller(t *testing.T, source *ObjectStoreSource, maxObjects int) (*Poller, *objectStoreTestDB) {
	t.Helper()
	dbPath := t.TempDir() + "/test.db"
	store, err := database.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(context.Background()); err != nil {
		store.Close()
		t.Fatal(err)
	}
	db := &objectStoreTestDB{store: store, dbPath: dbPath}
	db.cleanup = func() {
		if db.store != nil {
			_ = db.store.Close()
		}
	}
	t.Cleanup(func() { db.cleanup() })
	poller := NewPoller(nil, store, PollerConfig{
		PollInterval:       time.Hour,
		InitialBackfill:    time.Hour,
		Retention:          24 * time.Hour,
		CleanupInterval:    time.Hour,
		DeviceCacheRefresh: time.Hour,
		FlowBackend:        "s3",
		ObjectStore:        source.cfg,
	})
	poller.ConfigureObjectStore(source)
	if poller.config.ObjectStore.MaxObjects != maxObjects {
		t.Fatalf("test poller max objects = %d, want %d", poller.config.ObjectStore.MaxObjects, maxObjects)
	}
	return poller, db
}

func TestObjectStorePollCapsObjectsAndUsesDeterministicEqualTimestampOrder(t *testing.T) {
	objects := []testObject{
		testFlowObject(t, "network/2026/05/08/c/2026-05-08-13-45-00.ndjson", "node-c", 30, false),
		testFlowObject(t, "network/2026/05/08/a/2026-05-08-13-45-00.ndjson", "node-a", 10, false),
		testFlowObject(t, "network/2026/05/08/b/2026-05-08-13-45-00.ndjson", "node-b", 20, false),
	}
	source, server := newTestObjectStore(t, objects, 2)
	defer server.Close()
	poller, db := newObjectStoreTestPoller(t, source, 2)
	store := db.store
	ctx := context.Background()
	start := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 14, 0, 0, 0, time.UTC)

	if err := poller.pollObjectStore(ctx, start, end); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{objects[1].key, objects[2].key} {
		seen, err := store.IsObjectIngested(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if !seen {
			t.Fatalf("expected %s to be ingested", key)
		}
	}
	seen, err := store.IsObjectIngested(ctx, objects[0].key)
	if err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Fatalf("expected cap to defer %s", objects[0].key)
	}

	pairs, err := store.GetNodePairAggregates(ctx, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].TxBytes != 30 || pairs[0].FlowCount != 2 {
		t.Fatalf("first capped poll pairs = %+v, want 30 bytes and two flows", pairs)
	}
	state, err := store.GetPollState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	objectTime, _ := objectTime(objects[0].key)
	if !state.LastPollEnd.Equal(objectTime) {
		t.Fatalf("poll cursor = %v, want %v", state.LastPollEnd, objectTime)
	}

	if err := poller.pollObjectStore(ctx, state.LastPollEnd, end); err != nil {
		t.Fatal(err)
	}
	seen, err = store.IsObjectIngested(ctx, objects[0].key)
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatalf("expected deferred object %s to be ingested on repoll", objects[0].key)
	}
	pairs, err = store.GetNodePairAggregates(ctx, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].TxBytes != 60 || pairs[0].FlowCount != 3 {
		t.Fatalf("after repoll pairs = %+v, want 60 bytes and three flows", pairs)
	}
}

func TestObjectStoreRepollAndRestartAreIdempotentAndBackfillMetadata(t *testing.T) {
	key := "network/2026/05/08/2026-05-08-13-45-00.ndjson"
	source, server := newTestObjectStore(t, []testObject{
		testFlowObject(t, key, "node-a", 25, true),
	}, 10)
	defer server.Close()
	poller, db := newObjectStoreTestPoller(t, source, 10)
	store := db.store
	ctx := context.Background()
	start := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 14, 0, 0, 0, time.UTC)

	if err := poller.pollObjectStore(ctx, start, end); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db.store = nil
	metadataDB, err := sql.Open("sqlite", db.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := metadataDB.ExecContext(ctx, "DELETE FROM node_metadata"); err != nil {
		_ = metadataDB.Close()
		t.Fatal(err)
	}
	if err := metadataDB.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = database.NewSQLiteStore(db.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(ctx); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	db.store = store

	// A fresh poller simulates a process restart while retaining the database
	// ingestion guard and cursor.
	restarted := NewPoller(nil, store, poller.config)
	restarted.ConfigureObjectStore(source)
	if err := restarted.pollObjectStore(ctx, time.Date(2026, 5, 8, 13, 45, 0, 0, time.UTC), end); err != nil {
		t.Fatal(err)
	}

	pairs, err := store.GetNodePairAggregates(ctx, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].TxBytes != 25 || pairs[0].FlowCount != 1 {
		t.Fatalf("repoll double-counted object: %+v", pairs)
	}
	metadata, err := store.GetNodeMetadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 2 {
		t.Fatalf("metadata rows = %+v, want source and destination metadata", metadata)
	}
	state, err := store.GetPollState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastPollEnd.Equal(end) {
		t.Fatalf("restart cursor = %v, want %v after an all-seen poll", state.LastPollEnd, end)
	}

	if err := restarted.pollObjectStore(ctx, state.LastPollEnd, end); err != nil {
		t.Fatal(err)
	}
	pairs, err = store.GetNodePairAggregates(ctx, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].TxBytes != 25 || pairs[0].FlowCount != 1 {
		t.Fatalf("second repoll double-counted object: %+v", pairs)
	}
}

func TestObjectStoreRepairsPartiallyMissingMetadataOutsideLookback(t *testing.T) {
	key := "network/2026/05/08/2026-05-08-13-45-00.ndjson"
	source, server := newTestObjectStore(t, []testObject{
		testFlowObject(t, key, "node-a", 25, true),
	}, 10)
	defer server.Close()
	poller, db := newObjectStoreTestPoller(t, source, 10)
	store := db.store
	ctx := context.Background()
	start := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 14, 0, 0, 0, time.UTC)

	if err := poller.pollObjectStore(ctx, start, end); err != nil {
		t.Fatal(err)
	}
	metadataDB, err := sql.Open("sqlite", db.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer metadataDB.Close()
	if _, err := metadataDB.ExecContext(ctx, "DELETE FROM node_metadata WHERE node_id = ?", "node-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := metadataDB.ExecContext(ctx, "UPDATE ingested_objects SET metadata_hydrated = 1 WHERE object_key = ?", key); err != nil {
		t.Fatal(err)
	}

	// The object is outside the requested range and the source lookback. The
	// metadata index must still identify it for repair before listing objects.
	if err := poller.pollObjectStore(ctx, end, end.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	metadata, err := store.GetNodeMetadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 2 {
		t.Fatalf("metadata rows = %+v, want both nodes after repair", metadata)
	}
}

func TestObjectStorePollPropagatesCancellation(t *testing.T) {
	source, server := newTestObjectStore(t, nil, 10)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	poller, _ := newObjectStoreTestPoller(t, source, 10)
	start := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 14, 0, 0, 0, time.UTC)
	if err := poller.pollObjectStore(ctx, start, end); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("poll cancellation error = %v", err)
	}
}
