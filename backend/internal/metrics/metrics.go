// Package metrics serves Prometheus metrics about tsflow itself: pollers,
// storage, HTTP routes and MCP tools. It is off unless TSFLOW_METRICS_ADDR
// is set, and it listens on its own port so it never sits behind UI auth.
package metrics

import (
	"context"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

const namespace = "tsflow"

// scrapeQueryTimeout bounds the store reads one scrape makes.
const scrapeQueryTimeout = 5 * time.Second

// Store is the part of the SQLite store the scrape reads. Each call is a
// single-row read or a file stat.
type Store interface {
	GetPollStateTime(ctx context.Context, tailnetID string) (time.Time, error)
	NewestPairBucket(ctx context.Context, tailnetID string) (time.Time, bool, error)
	DiskBytes() int64
}

// Options configures New.
type Options struct {
	Version  string
	Registry *services.Registry
	Store    Store
	// Traffic adds traffic counters and the tag, user and port window
	// gauges (TSFLOW_METRICS_TRAFFIC).
	Traffic bool
	// TopTalkers lists this many top devices by bytes. 0 lists none.
	TopTalkers int
}

// Metrics owns a private Prometheus registry, so nothing else in the process
// adds series to /metrics by accident.
type Metrics struct {
	reg *prometheus.Registry

	httpDuration *prometheus.HistogramVec
	pollDuration *prometheus.HistogramVec
	mcpCalls     *prometheus.CounterVec
	mcpDuration  *prometheus.HistogramVec

	dbInitSeconds   prometheus.Gauge
	countersReady   prometheus.Gauge
	countersSeconds prometheus.Gauge
	collectErrors   prometheus.Counter

	traffic         bool
	trafficBytes    *prometheus.CounterVec
	trafficPackets  *prometheus.CounterVec
	trafficFlows    *prometheus.CounterVec
	trafficProtocol *prometheus.CounterVec
	trafficWindow   *trafficCollector
}

// HTTP buckets reach a minute because cold reads at large scale can take
// tens of seconds.
var httpBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60}

var pollBuckets = []float64{.1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600}

// New builds the registry and the collectors. Call Instrument on each poller
// through the registry before the pollers start.
func New(opts Options) *Metrics {
	m := &Metrics{reg: prometheus.NewRegistry()}
	m.httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "http_request_duration_seconds",
		Help:      "HTTP request latency by route template, method and status code.",
		Buckets:   httpBuckets,
	}, []string{"route", "method", "code"})
	m.pollDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "poll_duration_seconds",
		Help:      "Time one flow-log poll took, by tailnet and result.",
		Buckets:   pollBuckets,
	}, []string{"tailnet", "result"})
	m.mcpCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "mcp_tool_calls_total",
		Help:      "MCP tool calls by tool and result.",
	}, []string{"tool", "result"})
	m.mcpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "mcp_tool_duration_seconds",
		Help:      "MCP tool call latency by tool.",
		Buckets:   httpBuckets,
	}, []string{"tool"})
	m.dbInitSeconds = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "db_init_duration_seconds",
		Help:      "Time the startup schema migrations and rollup backfills took.",
	})
	m.countersReady = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "pair_row_counters_ready",
		Help:      "1 once the per-minute pair row counters are built, 0 while the background rebuild runs.",
	})
	m.countersSeconds = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "pair_row_counters_build_duration_seconds",
		Help:      "Time the background pair row counter build took. 0 until it finishes.",
	})
	m.collectErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "metrics_collect_errors_total",
		Help:      "Store reads that failed while serving /metrics.",
	})
	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "build_info",
		Help:      "Always 1. The labels carry the tsflow and Go versions.",
	}, []string{"version", "goversion"})
	version := opts.Version
	if version == "" {
		version = "dev"
	}
	buildInfo.WithLabelValues(version, runtime.Version()).Set(1)

	m.reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo,
		m.httpDuration, m.pollDuration, m.mcpCalls, m.mcpDuration,
		m.dbInitSeconds, m.countersReady, m.countersSeconds, m.collectErrors,
		&pollerCollector{registry: opts.Registry, store: opts.Store, errors: m.collectErrors},
	)
	if opts.Traffic {
		m.traffic = true
		m.newTrafficCounters()
	}
	if opts.Traffic || opts.TopTalkers > 0 {
		m.trafficWindow = &trafficCollector{
			breakdowns: opts.Traffic,
			topTalkers: opts.TopTalkers,
			now:        time.Now,
		}
		if opts.Registry != nil {
			m.trafficWindow.sources = registrySources(opts.Registry)
		}
		m.reg.MustRegister(m.trafficWindow)
	}
	return m
}

// Registry is the Prometheus registry behind Handler. Later collectors
// register here.
func (m *Metrics) Registry() *prometheus.Registry { return m.reg }

// Handler serves the registry in the Prometheus text or OpenMetrics format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
		Registry:          m.reg,
	})
}

// Instrument attaches the poll duration histogram, and the traffic counters
// when they are on, to every poller.
func (m *Metrics) Instrument(reg *services.Registry) {
	for _, entry := range reg.List() {
		if entry == nil || entry.Poller == nil {
			continue
		}
		ok := m.pollDuration.WithLabelValues(entry.ID, "success")
		failed := m.pollDuration.WithLabelValues(entry.ID, "error")
		entry.Poller.SetPollObserver(func(d time.Duration, err error) {
			if err != nil {
				failed.Observe(d.Seconds())
				return
			}
			ok.Observe(d.Seconds())
		})
		if m.traffic {
			entry.Poller.SetIngestObserver(m.trafficObserver(entry.ID))
		}
	}
}

// SetDBInitDuration records how long store.Init took.
func (m *Metrics) SetDBInitDuration(d time.Duration) { m.dbInitSeconds.Set(d.Seconds()) }

// PairRowCountersDone records the end of the background counter build. A
// failed build leaves ready at 0.
func (m *Metrics) PairRowCountersDone(d time.Duration, err error) {
	if err != nil {
		return
	}
	m.countersSeconds.Set(d.Seconds())
	m.countersReady.Set(1)
}

// ObserveMCPTool records one MCP tool call. Unknown tool names are folded
// into "unknown" by the caller so the label stays bounded.
func (m *Metrics) ObserveMCPTool(tool string, d time.Duration, failed bool) {
	result := "success"
	if failed {
		result = "error"
	}
	m.mcpCalls.WithLabelValues(tool, result).Inc()
	m.mcpDuration.WithLabelValues(tool).Observe(d.Seconds())
}

// Middleware times each request under its route template, never the raw
// path, so ids in the URL do not create series. Requests that match no
// route, including the embedded UI's pages, share route="other".
func (m *Metrics) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "other"
		}
		m.httpDuration.WithLabelValues(route, method(c.Request.Method), statusCode(c.Writer.Status())).
			Observe(time.Since(start).Seconds())
	}
}

func method(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return m
	}
	return "OTHER"
}

var codeLabels sync.Map

func statusCode(code int) string {
	if v, ok := codeLabels.Load(code); ok {
		return v.(string)
	}
	s := strconv.Itoa(code)
	if code < 100 || code > 599 {
		s = "other"
	}
	codeLabels.Store(code, s)
	return s
}
