package services

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/audit"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
)

type memAuditBlobs struct {
	mu      sync.Mutex
	objects map[string][]byte
	listed  []string
	failOn  string
}

func (m *memAuditBlobs) List(_ context.Context, prefix string) ([]blobObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listed = append(m.listed, prefix)
	var out []blobObject
	for key, body := range m.objects {
		if strings.HasPrefix(key, prefix) {
			out = append(out, blobObject{Key: key, Size: int64(len(body)), LastModified: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)})
		}
	}
	return out, nil
}

func (m *memAuditBlobs) Open(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key == m.failOn {
		return nil, errors.New("temporary outage")
	}
	body, ok := m.objects[key]
	if !ok {
		return nil, errObjectGone
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func auditLine(at time.Time, login, action, targetType, targetID string) string {
	return fmt.Sprintf(`{"eventTime":%q,"type":"CONFIG","eventGroupID":"g%d","origin":"NODE","actor":{"id":"u1","type":"USER","loginName":%q},"target":{"id":%q,"name":"device-%s","type":%q},"action":%q}`,
		at.UTC().Format(time.RFC3339Nano), at.UnixNano(), login, targetID, targetID, targetType, action)
}

func gz(t *testing.T, lines ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	io.WriteString(w, strings.Join(lines, "\n")+"\n")
	w.Close()
	return buf.Bytes()
}

func newAuditTestStore(t *testing.T) *audit.Store {
	t.Helper()
	s, err := audit.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAuditPollerObjects(t *testing.T) {
	store := newAuditTestStore(t)
	now := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	t1 := now.Add(-2 * time.Hour)
	t2 := now.Add(-1 * time.Hour)
	blobs := &memAuditBlobs{objects: map[string][]byte{
		"configuration/2026/03/02/" + t1.Format("2006-01-02-15-04-05") + ".ndjson.gz": gz(t,
			auditLine(t1, "ada@example.com", "LOGIN", "NODE", "nA"),
			"not json",
			auditLine(t1.Add(time.Second), "ada@example.com", "LOGIN", "ADMIN_CONSOLE", ""),
		),
		"configuration/2026/03/02/" + t2.Format("2006-01-02-15-04-05") + ".json": []byte(auditLine(t2, "bob@example.com", "LOGIN", "NODE", "nB")),
		// A key that is not named by time falls back to its upload time.
		"configuration/2026/03/02/extra-batch.json": []byte(auditLine(t2.Add(time.Minute), "bob@example.com", "LOGOUT", "NODE", "nB")),
		// Another tailnet's prefix must not be read.
		"other/2026/03/02/2026-03-02-11-00-00.json": []byte(auditLine(t2, "eve@example.com", "LOGIN", "NODE", "nE")),
	}}
	p := newAuditPoller("t1", config.AuditSource{Backend: config.AuditBackendGCS, Prefix: "configuration/"}, store, config.Audit{Backfill: 24 * time.Hour})
	p.blobs = blobs
	p.now = func() time.Time { return now }

	ctx := context.Background()
	if err := p.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	st := p.Status()
	if st.EventsIngested != 4 || st.ObjectsIngested != 3 || st.LastError != "" {
		t.Fatalf("status after first poll: %+v", st)
	}
	page, _ := store.Query(ctx, audit.Filter{TailnetID: "t1"})
	if len(page.Events) != 4 {
		t.Fatalf("stored %d events, want 4", len(page.Events))
	}
	for _, e := range page.Events {
		if e.Actor.LoginName == "eve@example.com" {
			t.Fatal("read an object outside the tailnet prefix")
		}
	}
	cursor, _ := store.Cursor(ctx, "t1")
	if !cursor.Equal(now) {
		t.Fatalf("cursor %v, want %v", cursor, now)
	}

	// A second poll reads nothing new.
	if err := p.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if p.Status().EventsIngested != 4 {
		t.Fatalf("second poll re-ingested: %+v", p.Status())
	}
}

func TestAuditPollerObjectFailureHoldsCursor(t *testing.T) {
	store := newAuditTestStore(t)
	now := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	t1 := now.Add(-2 * time.Hour)
	t2 := now.Add(-1 * time.Hour)
	bad := "configuration/2026/03/02/" + t1.Format("2006-01-02-15-04-05") + ".json"
	blobs := &memAuditBlobs{failOn: bad, objects: map[string][]byte{
		bad: []byte(auditLine(t1, "ada@example.com", "LOGIN", "NODE", "nA")),
		"configuration/2026/03/02/" + t2.Format("2006-01-02-15-04-05") + ".json": []byte(auditLine(t2, "bob@example.com", "LOGIN", "NODE", "nB")),
	}}
	p := newAuditPoller("t1", config.AuditSource{Backend: config.AuditBackendS3, Prefix: "configuration/"}, store, config.Audit{Backfill: 24 * time.Hour})
	p.blobs = blobs
	p.now = func() time.Time { return now }
	ctx := context.Background()
	if err := p.PollOnce(ctx); err == nil {
		t.Fatal("expected the outage to be reported")
	}
	if p.Status().EventsIngested != 1 || p.Status().LastError == "" {
		t.Fatalf("later objects should still be read: %+v", p.Status())
	}
	cursor, _ := store.Cursor(ctx, "t1")
	if !cursor.Equal(t1) {
		t.Fatalf("cursor should stay at the unreadable object: %v", cursor)
	}
	blobs.failOn = ""
	if err := p.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if p.Status().EventsIngested != 2 || p.Status().LastError != "" {
		t.Fatalf("retry should read the repaired object: %+v", p.Status())
	}
}

type fakeAuditAPI struct {
	calls []string
	lines []string
	times []time.Time
}

func (f *fakeAuditAPI) GetConfigurationAuditLogs(_ context.Context, start, end time.Time) ([]byte, error) {
	f.calls = append(f.calls, start.Format(time.RFC3339)+"/"+end.Format(time.RFC3339))
	var picked []string
	for i, at := range f.times {
		if !at.Before(start) && !at.After(end) {
			picked = append(picked, f.lines[i])
		}
	}
	return []byte(`{"version":"1.1","tailnetId":"example.com","logs":[` + strings.Join(picked, ",") + `]}`), nil
}

func TestAuditPollerAPI(t *testing.T) {
	store := newAuditTestStore(t)
	now := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	api := &fakeAuditAPI{}
	for i := 0; i < 5; i++ {
		at := now.Add(-time.Duration(i*30+1) * time.Hour)
		api.times = append(api.times, at)
		api.lines = append(api.lines, auditLine(at, "ada@example.com", "LOGIN", "NODE", fmt.Sprintf("n%d", i)))
	}
	p := newAuditPoller("t1", config.AuditSource{Backend: config.AuditBackendAPI}, store, config.Audit{Backfill: 7 * 24 * time.Hour})
	p.api = api
	p.now = func() time.Time { return now }
	ctx := context.Background()
	if err := p.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := p.Status().EventsIngested; got != 5 {
		t.Fatalf("ingested %d, want 5", got)
	}
	if len(api.calls) < 7 || len(api.calls) > 8 {
		t.Fatalf("a week should be read a day at a time, got %d calls", len(api.calls))
	}
	// The next poll re-reads only the overlap, and overlapping records are
	// dropped by hash.
	api.calls = nil
	if err := p.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(api.calls) != 1 || p.Status().EventsIngested != 5 {
		t.Fatalf("second poll: calls=%v status=%+v", api.calls, p.Status())
	}
}

func TestAuditManagerResolvesSources(t *testing.T) {
	store := newAuditTestStore(t)
	specs := []config.TailnetSpec{
		{ID: "a", Audit: &config.AuditSource{Backend: config.AuditBackendOff}},
		{ID: "b"},
	}
	m, err := NewAuditManager(context.Background(), config.Audit{Default: config.AuditSource{Backend: config.AuditBackendAPI}}, specs, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		st, ok := m.Status(id)
		if !ok || st.Backend != config.AuditBackendOff || st.Running {
			t.Fatalf("%s: %+v %v", id, st, ok)
		}
	}
	// api with no registry entry has no credentials and must fail clearly.
	_, err = NewAuditManager(context.Background(), config.Audit{}, []config.TailnetSpec{{ID: "c", Audit: &config.AuditSource{Backend: config.AuditBackendAPI}}}, nil, store)
	if err == nil || !strings.Contains(err.Error(), "logs:configuration:read") {
		t.Fatalf("expected a credentials error, got %v", err)
	}
	_, err = NewAuditManager(context.Background(), config.Audit{}, []config.TailnetSpec{{ID: "d", Audit: &config.AuditSource{Backend: config.AuditBackendGCS}}}, nil, store)
	if err == nil || !strings.Contains(err.Error(), "bucket is required") {
		t.Fatalf("expected a bucket error, got %v", err)
	}
}

func TestAuditObjectTime(t *testing.T) {
	want := time.Date(2026, 3, 2, 10, 5, 0, 0, time.UTC)
	for _, key := range []string{
		"p/2026/03/02/2026-03-02-10-05-00.json",
		"p/2026/03/02/2026-03-02-10-05-00.json.gz",
		"p/2026/03/02/2026-03-02-10-05-00.ndjson.zst",
		"p/2026/03/02/2026-03-02-10-05-00.gz",
	} {
		got, ok := auditObjectTime(key)
		if !ok || !got.Equal(want) {
			t.Errorf("%s: %v %v", key, got, ok)
		}
	}
	if _, ok := auditObjectTime("p/2026/03/02/batch.json"); ok {
		t.Error("a name without a time should not parse")
	}
}
