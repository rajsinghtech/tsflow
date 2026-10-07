package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

type logsAPIRecorder struct {
	mu     sync.Mutex
	starts []time.Time
	ends   []time.Time
}

func (r *logsAPIRecorder) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start, err1 := time.Parse(time.RFC3339Nano, req.URL.Query().Get("start"))
		end, err2 := time.Parse(time.RFC3339Nano, req.URL.Query().Get("end"))
		if err1 != nil || err2 != nil {
			t.Errorf("bad window %q", req.URL.RawQuery)
		}
		r.mu.Lock()
		r.starts = append(r.starts, start)
		r.ends = append(r.ends, end)
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"logs":[]}`))
	})
}

func newDelayPoller(t *testing.T, delay time.Duration) (*Poller, *logsAPIRecorder, *database.SQLiteStore) {
	t.Helper()
	rec := &logsAPIRecorder{}
	server := httptest.NewServer(rec.handler(t))
	t.Cleanup(server.Close)
	store, err := database.NewSQLiteStore(t.TempDir() + "/poller.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := NewTailscaleService(&config.Config{TailscaleAPIURL: server.URL, TailscaleTailnet: "example.com"})
	cfg := DefaultPollerConfig()
	cfg.PollDelay = delay
	cfg.InitialBackfill = 10 * time.Minute
	return NewPoller(service, store, cfg), rec, store
}

// API polls end PollDelay before now, so logs the API has not published yet
// are fetched on a later poll instead of being skipped by the cursor.
func TestAPIPollEndsBeforeTheDelay(t *testing.T) {
	poller, rec, store := newDelayPoller(t, 2*time.Minute)
	before := time.Now()
	if err := poller.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := time.Now()
	if len(rec.ends) != 1 {
		t.Fatalf("API calls = %d, want 1", len(rec.ends))
	}
	end := rec.ends[0]
	if end.Before(before.Add(-2*time.Minute).Add(-time.Second)) || end.After(after.Add(-2*time.Minute).Add(time.Second)) {
		t.Fatalf("poll end = %s, want about now-2m (%s)", end, before.Add(-2*time.Minute))
	}
	state, err := store.GetPollState(context.Background(), database.DefaultTailnetID)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastPollEnd.Sub(end).Abs() > time.Second {
		t.Fatalf("cursor = %s, want the delayed end %s", state.LastPollEnd, end)
	}

	// Right after that poll, the next window is still inside the delay.
	// Nothing is requested and the cursor stays put.
	if err := store.UpdatePollState(context.Background(), database.DefaultTailnetID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := poller.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rec.ends) != 1 {
		t.Fatalf("API calls = %d, want no request while the cursor is inside the delay", len(rec.ends))
	}
}

func TestAPIPollDelayZeroPollsToNow(t *testing.T) {
	poller, rec, _ := newDelayPoller(t, 0)
	before := time.Now()
	if err := poller.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rec.ends) != 1 || rec.ends[0].Before(before.Add(-time.Second)) {
		t.Fatalf("ends = %v, want about now", rec.ends)
	}
}

func TestPollDelayConfig(t *testing.T) {
	if DefaultPollerConfig().PollDelay != 2*time.Minute {
		t.Fatalf("default delay = %s", DefaultPollerConfig().PollDelay)
	}
	cfg := DefaultPollerConfig()
	cfg.PollDelay = -time.Second
	if err := cfg.validate(); err == nil {
		t.Fatal("negative delay accepted")
	}
	for raw, want := range map[string]time.Duration{"": 2 * time.Minute, "2m": 2 * time.Minute, "0": 0, "45s": 45 * time.Second} {
		pc, err := PollerConfigFrom(&config.Config{PollInterval: "5m", InitialBackfill: "6h", PollDelay: raw})
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		if pc.PollDelay != want {
			t.Fatalf("TSFLOW_POLL_DELAY=%q -> %s, want %s", raw, pc.PollDelay, want)
		}
	}
	if _, err := PollerConfigFrom(&config.Config{PollInterval: "5m", InitialBackfill: "6h", PollDelay: "soon"}); err == nil {
		t.Fatal("invalid delay accepted")
	}
}
