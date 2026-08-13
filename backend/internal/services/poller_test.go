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
