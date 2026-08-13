package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
)

func TestPollerStopCancelsInitialDeviceRefresh(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	service := NewTailscaleService(&config.Config{
		TailscaleAPIURL:  server.URL,
		TailscaleAPIKey:  "test-key",
		TailscaleTailnet: "example.com",
	})
	poller := NewPoller(service, nil, DefaultPollerConfig())
	startedPoller := make(chan error, 1)
	go func() { startedPoller <- poller.Start(context.Background()) }()

	select {
	case <-started:
	case <-time.After(time.Second):
		poller.Stop()
		t.Fatal("initial device refresh did not reach test server")
	}

	stopped := make(chan struct{})
	go func() {
		poller.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("poller Stop did not cancel the in-flight refresh")
	}

	select {
	case err := <-startedPoller:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("poller Start did not return after cancellation")
	}
}

func TestPollerStartDoesNotWaitForDeviceRefresh(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	service := NewTailscaleService(&config.Config{
		TailscaleAPIURL:  server.URL,
		TailscaleAPIKey:  "test-key",
		TailscaleTailnet: "example.com",
	})
	poller := NewPoller(service, nil, DefaultPollerConfig())

	startReturned := make(chan error, 1)
	go func() { startReturned <- poller.Start(context.Background()) }()
	select {
	case err := <-startReturned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(250 * time.Millisecond):
		poller.Stop()
		t.Fatal("poller Start waited for the device refresh")
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		poller.Stop()
		t.Fatal("background device refresh did not start")
	}
	poller.Stop()
}

func TestPollerStartWithCanceledContextStopsCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	poller := NewPoller(nil, nil, DefaultPollerConfig())

	if err := poller.Start(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if running, ok := poller.Stats()["running"].(bool); ok && !running {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("poller remained running after startup context cancellation")
}

func TestPollerStopIsSafeWhenCalledConcurrently(t *testing.T) {
	poller := NewPoller(nil, nil, DefaultPollerConfig())
	if err := poller.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	stopped := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			poller.Stop()
			stopped <- struct{}{}
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("concurrent poller Stop did not return")
		}
	}
}

func TestPollerStartRejectsUnsafeIntervals(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*PollerConfig)
	}{
		{name: "zero poll interval", edit: func(c *PollerConfig) { c.PollInterval = 0 }},
		{name: "negative cleanup interval", edit: func(c *PollerConfig) { c.CleanupInterval = -time.Second }},
		{name: "negative backfill", edit: func(c *PollerConfig) { c.InitialBackfill = -time.Second }},
		{name: "zero backfill", edit: func(c *PollerConfig) { c.InitialBackfill = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultPollerConfig()
			tc.edit(&cfg)
			poller := NewPoller(nil, nil, cfg)
			if err := poller.Start(context.Background()); err == nil {
				t.Fatal("Start() unexpectedly accepted unsafe configuration")
			}
		})
	}
}
