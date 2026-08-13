package services

import (
	"bytes"
	"compress/gzip"
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
	"github.com/klauspost/compress/zstd"
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
	return newTestObjectStoreWithPageSize(t, objects, maxObjects, 0)
}

func newTestObjectStoreWithPageSize(t *testing.T, objects []testObject, maxObjects, pageSize int) (*ObjectStoreSource, *httptest.Server) {
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

			pageStart := 0
			if token := r.URL.Query().Get("continuation-token"); token != "" {
				const tokenPrefix = "page-"
				if !strings.HasPrefix(token, tokenPrefix) {
					http.Error(w, "invalid continuation token", http.StatusBadRequest)
					return
				}
				var err error
				pageStart, err = strconv.Atoi(strings.TrimPrefix(token, tokenPrefix))
				if err != nil || pageStart < 0 || pageStart > len(keys) {
					http.Error(w, "invalid continuation token", http.StatusBadRequest)
					return
				}
			}
			pageEnd := len(keys)
			if pageSize > 0 && pageStart+pageSize < pageEnd {
				pageEnd = pageStart + pageSize
			}
			pageKeys := keys[pageStart:pageEnd]

			var response bytes.Buffer
			response.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
			response.WriteString(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
			response.WriteString(`<Name>bucket</Name><KeyCount>`)
			response.WriteString(strconv.Itoa(len(pageKeys)))
			response.WriteString(`</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>`)
			response.WriteString(strconv.FormatBool(pageEnd < len(keys)))
			response.WriteString(`</IsTruncated>`)
			if pageEnd < len(keys) {
				response.WriteString(`<NextContinuationToken>page-`)
				response.WriteString(strconv.Itoa(pageEnd))
				response.WriteString(`</NextContinuationToken>`)
			}
			for _, key := range pageKeys {
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
	return testFlowObjectAt(t, key, nodeID, time.Date(2026, 5, 8, 13, 45, 0, 0, time.UTC), bytes, withMetadata)
}

func testFlowObjectAt(t *testing.T, key, nodeID string, loggedAt time.Time, byteCount int64, withMetadata bool) testObject {
	t.Helper()
	logMap := map[string]any{
		"nodeId": nodeID,
		"start":  loggedAt.UTC().Format(time.RFC3339),
		"virtualTraffic": []any{map[string]any{
			"proto":   6,
			"src":     "100.64.0.1:1234",
			"dst":     "100.64.0.2:443",
			"txBytes": byteCount,
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

func compressedTestFlowObject(t *testing.T, key, nodeID string, loggedAt time.Time, byteCount int64, suffix string) testObject {
	t.Helper()
	object := testFlowObjectAt(t, key, nodeID, loggedAt, byteCount, false)
	if suffix == "" {
		return object
	}

	var compressed bytes.Buffer
	switch suffix {
	case ".gz", ".gzip":
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(object.body); err != nil {
			t.Fatalf("compress gzip object: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close gzip object: %v", err)
		}
	case ".zst", ".zstd":
		writer, err := zstd.NewWriter(&compressed)
		if err != nil {
			t.Fatalf("create zstd writer: %v", err)
		}
		if _, err := writer.Write(object.body); err != nil {
			t.Fatalf("compress zstd object: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close zstd object: %v", err)
		}
	default:
		t.Fatalf("unsupported test compression suffix %q", suffix)
	}
	object.body = compressed.Bytes()
	return object
}

func newObjectStoreTestPollerWithService(t *testing.T, source *ObjectStoreSource, maxObjects int, tsService *TailscaleService) (*Poller, *objectStoreTestDB) {
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
	poller := NewPoller(tsService, store, PollerConfig{
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

func newObjectStoreTestPoller(t *testing.T, source *ObjectStoreSource, maxObjects int) (*Poller, *objectStoreTestDB) {
	return newObjectStoreTestPollerWithService(t, source, maxObjects, nil)
}

func TestObjectStorePollsRealGzipAndZstdBodies(t *testing.T) {
	base := time.Date(2026, 5, 8, 13, 45, 0, 0, time.UTC)
	suffixes := []string{".gz", ".gzip", ".zst", ".zstd"}
	objects := make([]testObject, 0, len(suffixes))
	for index, suffix := range suffixes {
		key := "network/2026/05/08/2026-05-08-13-45-0" + strconv.Itoa(index) + ".ndjson" + suffix
		objects = append(objects, compressedTestFlowObject(t, key, "node-compressed", base, int64(index+1), suffix))
	}
	source, server := newTestObjectStore(t, objects, len(objects))
	defer server.Close()
	poller, db := newObjectStoreTestPoller(t, source, len(objects))
	ctx := context.Background()
	if err := poller.pollObjectStore(ctx, base.Add(-time.Minute), base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	for _, object := range objects {
		seen, err := db.store.IsObjectIngested(ctx, object.key)
		if err != nil {
			t.Fatal(err)
		}
		if !seen {
			t.Fatalf("expected compressed object %s to be ingested", object.key)
		}
	}
	pairs, err := db.store.GetNodePairAggregates(ctx, base.Add(-time.Minute), base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].TxBytes != 10 || pairs[0].FlowCount != int64(len(objects)) {
		t.Fatalf("compressed aggregate = %+v, want 10 bytes and %d flows", pairs, len(objects))
	}
}

func TestObjectStorePollPaginatesAndUsesHalfOpenEnd(t *testing.T) {
	base := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)
	objects := []testObject{
		testFlowObjectAt(t, "network/2026/05/08/a/2026-05-08-13-00-00.ndjson", "node-a", base, 10, false),
		testFlowObjectAt(t, "network/2026/05/08/b/2026-05-08-13-30-00.ndjson", "node-b", base.Add(30*time.Minute), 20, false),
		testFlowObjectAt(t, "network/2026/05/08/c/2026-05-08-14-00-00.ndjson", "node-c", base.Add(time.Hour), 40, false),
	}
	source, server := newTestObjectStoreWithPageSize(t, objects, 10, 1)
	defer server.Close()
	poller, db := newObjectStoreTestPoller(t, source, 10)
	ctx := context.Background()
	end := base.Add(time.Hour)
	if err := poller.pollObjectStore(ctx, base, end); err != nil {
		t.Fatal(err)
	}

	for _, object := range objects[:2] {
		seen, err := db.store.IsObjectIngested(ctx, object.key)
		if err != nil {
			t.Fatal(err)
		}
		if !seen {
			t.Fatalf("expected in-range object %s to be ingested", object.key)
		}
	}
	seen, err := db.store.IsObjectIngested(ctx, objects[2].key)
	if err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Fatalf("object at exclusive end %s was ingested", objects[2].key)
	}
	pairs, err := db.store.GetNodePairAggregates(ctx, base, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].TxBytes != 30 || pairs[0].FlowCount != 2 {
		t.Fatalf("paginated aggregate = %+v, want 30 bytes and two flows", pairs)
	}
}

func TestObjectStoreContinuesAfterMalformedCompressedObject(t *testing.T) {
	base := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)
	badKey := "network/2026/05/08/a/2026-05-08-13-00-00.ndjson.zstd"
	goodKey := "network/2026/05/08/b/2026-05-08-13-30-00.ndjson.gz"
	source, server := newTestObjectStore(t, []testObject{
		{key: badKey, body: []byte("this is not zstd")},
		compressedTestFlowObject(t, goodKey, "node-good", base.Add(30*time.Minute), 20, ".gz"),
	}, 10)
	defer server.Close()
	poller, db := newObjectStoreTestPoller(t, source, 10)
	ctx := context.Background()
	err := poller.pollObjectStore(ctx, base, base.Add(time.Hour))
	if err == nil || !strings.Contains(err.Error(), badKey) {
		t.Fatalf("malformed compression error = %v, want error naming %s", err, badKey)
	}
	seen, err := db.store.IsObjectIngested(ctx, goodKey)
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatalf("expected valid compressed object %s to be ingested", goodKey)
	}
	pairs, err := db.store.GetNodePairAggregates(ctx, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].TxBytes != 20 {
		t.Fatalf("aggregate after malformed compression = %+v, want valid object traffic only", pairs)
	}
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

func TestObjectStoreContinuesWhenHistoricalMetadataObjectIsMalformed(t *testing.T) {
	oldKey := "network/2026/05/08/a/2026-05-08-13-30-00.ndjson"
	newKey := "network/2026/05/08/b/2026-05-08-13-45-00.ndjson"
	objects := []testObject{
		{key: oldKey, body: []byte("{malformed\n")},
		testFlowObject(t, newKey, "node-b", 20, false),
	}
	source, server := newTestObjectStore(t, objects, 10)
	defer server.Close()
	poller, db := newObjectStoreTestPoller(t, source, 10)
	ctx := context.Background()
	base := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)

	// Seed the malformed object as an already-ingested row needing metadata
	// repair. It must not block ingestion of the newer valid object.
	if err := db.store.CommitObjectIngest(ctx, database.ObjectIngestResult{
		Key:          oldKey,
		LastModified: base,
		PollEnd:      base.Add(30 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	metadataDB, err := sql.Open("sqlite", db.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := metadataDB.ExecContext(ctx,
		"UPDATE ingested_objects SET metadata_hydrated = 0 WHERE object_key = ?", oldKey,
	); err != nil {
		_ = metadataDB.Close()
		t.Fatal(err)
	}
	if err := metadataDB.Close(); err != nil {
		t.Fatal(err)
	}

	if err := poller.pollObjectStore(ctx, base, base.Add(time.Hour)); err != nil {
		t.Fatalf("metadata repair should not block newer ingestion: %v", err)
	}
	seen, err := db.store.IsObjectIngested(ctx, newKey)
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatalf("expected valid object %s to be ingested", newKey)
	}
	pairs, err := db.store.GetNodePairAggregates(ctx, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].TxBytes != 20 {
		t.Fatalf("pairs = %+v, want valid object traffic despite malformed metadata object", pairs)
	}
}

func TestObjectStoreProcessesLaterObjectsWhenEarlierCandidateIsMalformed(t *testing.T) {
	oldKey := "network/2026/05/08/a/2026-05-08-13-30-00.ndjson"
	newKey := "network/2026/05/08/b/2026-05-08-13-45-00.ndjson"
	source, server := newTestObjectStore(t, []testObject{
		{key: oldKey, body: []byte("{malformed\n")},
		testFlowObject(t, newKey, "node-b", 20, false),
	}, 10)
	defer server.Close()
	poller, db := newObjectStoreTestPoller(t, source, 10)
	ctx := context.Background()
	base := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)

	err := poller.pollObjectStore(ctx, base, base.Add(time.Hour))
	if err == nil || !strings.Contains(err.Error(), oldKey) {
		t.Fatalf("malformed candidate error = %v, want error naming %s", err, oldKey)
	}
	seen, err := db.store.IsObjectIngested(ctx, newKey)
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatalf("expected later valid object %s to be ingested", newKey)
	}
	state, err := db.store.GetPollState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastPollEnd.Equal(base.Add(30 * time.Minute)) {
		t.Fatalf("poll cursor = %v, want earliest unreadable object at %v", state.LastPollEnd, base.Add(30*time.Minute))
	}
	pairs, err := db.store.GetNodePairAggregates(ctx, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].TxBytes != 20 {
		t.Fatalf("pairs = %+v, want later valid object traffic", pairs)
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
