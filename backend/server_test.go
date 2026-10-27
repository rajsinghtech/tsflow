package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestHTTPServerDropsSlowHeaderClients(t *testing.T) {
	old := serverReadHeaderTimeout
	serverReadHeaderTimeout = 200 * time.Millisecond
	t.Cleanup(func() { serverReadHeaderTimeout = old })

	srv := newHTTPServer("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	if srv.ReadHeaderTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Fatalf("timeouts not set: header=%v idle=%v", srv.ReadHeaderTimeout, srv.IdleTimeout)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	// A complete request is served.
	resp, err := http.Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}

	// A client that never finishes its headers is disconnected.
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\n"); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	_, err = bufio.NewReader(conn).ReadString('\n')
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("connection still open after %v; header timeout not enforced", time.Since(start))
	}
}

func TestMetricsServerServesOnlyMetrics(t *testing.T) {
	srv := newMetricsServer("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "tsflow_build_info 1\n")
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	base := "http://" + ln.Addr().String()
	for path, want := range map[string]int{
		"/metrics":     http.StatusOK,
		"/":            http.StatusNotFound,
		"/api/devices": http.StatusNotFound,
		"/health":      http.StatusNotFound,
		"/mcp":         http.StatusNotFound,
	} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
	}
	resp, err := http.Post(base+"/metrics", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics = %d", resp.StatusCode)
	}
}
