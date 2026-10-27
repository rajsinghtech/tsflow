package main

import (
	"net/http"
	"time"
)

// Header and idle limits for the HTTP listeners. Without a header timeout a
// client can hold a connection open forever by sending headers slowly. There
// is no write timeout because large aggregated queries can take minutes.
var (
	serverReadHeaderTimeout = 10 * time.Second
	serverIdleTimeout       = 2 * time.Minute
)

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		IdleTimeout:       serverIdleTimeout,
	}
}

// newMetricsServer serves only /metrics. Every other path is a 404, so the
// metrics port never exposes a data route.
func newMetricsServer(addr string, metrics http.Handler) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics)
	return newHTTPServer(addr, mux)
}
