package metrics

import (
	"bufio"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

// BenchmarkTrafficObserve is the work traffic metrics add to one poll: a
// pass over a 20k-device minute of node pairs (10,000 rows).
func BenchmarkTrafficObserve(b *testing.B) {
	m := New(Options{Traffic: true})
	observe := m.trafficObserver("default")
	pairs := make([]database.NodePairAggregate, 10000)
	for i := range pairs {
		typ := "virtual"
		if i%7 == 0 {
			typ = "subnet"
		}
		pairs[i] = pair(1, "a", "b", typ, int64(i), int64(i))
	}
	stats := []database.TrafficStats{{Bucket: 1, TCPBytes: 1}}
	b.ReportAllocs()
	for b.Loop() {
		observe(pairs, stats)
	}
}

// BenchmarkTrafficScrape20k is one uncached /metrics scrape at 20,000
// devices with a full 15-minute window (150,000 cached pair rows), traffic
// breakdowns and 20 top talkers, through the HTTP handler.
func BenchmarkTrafficScrape20k(b *testing.B) {
	rc, dc := syntheticTailnet(time.Now(), 20000, 500, 3000, 2000, 15)
	m := New(Options{Traffic: true, TopTalkers: 20})
	m.trafficWindow.sources = func() []trafficSource { return []trafficSource{{"default", rc, dc}} }
	m.trafficObserver("default")
	h := m.Handler()
	var series, size int
	b.ReportAllocs()
	for b.Loop() {
		m.trafficWindow.mu.Lock()
		m.trafficWindow.cached = nil
		m.trafficWindow.mu.Unlock()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
		size = rec.Body.Len()
		series = 0
		sc := bufio.NewScanner(rec.Body)
		for sc.Scan() {
			if line := sc.Text(); line != "" && !strings.HasPrefix(line, "#") {
				series++
			}
		}
	}
	b.ReportMetric(float64(series), "series")
	b.ReportMetric(float64(size), "bytes/scrape")
}

// BenchmarkCachedScrape20k is a scrape inside the cache TTL, which is what
// Prometheus sees most of the time.
func BenchmarkCachedScrape20k(b *testing.B) {
	rc, dc := syntheticTailnet(time.Now(), 20000, 500, 3000, 2000, 15)
	m := New(Options{Traffic: true, TopTalkers: 20})
	m.trafficWindow.sources = func() []trafficSource { return []trafficSource{{"default", rc, dc}} }
	h := m.Handler()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/metrics", nil))
	b.ReportAllocs()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/metrics", nil))
	}
}
