package metrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

var (
	pollerUpDesc = prometheus.NewDesc(namespace+"_poller_up",
		"1 while the tailnet's poll loop is running.", []string{"tailnet", "backend"}, nil)
	lastSuccessDesc = prometheus.NewDesc(namespace+"_poller_last_success_timestamp_seconds",
		"Unix time of the tailnet's last successful poll. 0 before the first one.", []string{"tailnet"}, nil)
	pollErrorsDesc = prometheus.NewDesc(namespace+"_poller_errors_total",
		"Polls that failed since start.", []string{"tailnet"}, nil)
	ingestedDesc = prometheus.NewDesc(namespace+"_poller_ingested_flow_logs_total",
		"Flow log rows ingested since start.", []string{"tailnet"}, nil)
	lastPollRowsDesc = prometheus.NewDesc(namespace+"_poller_last_poll_flow_logs",
		"Flow log rows in the most recent poll.", []string{"tailnet"}, nil)
	cursorDesc = prometheus.NewDesc(namespace+"_poller_cursor_timestamp_seconds",
		"Unix time the next poll starts from. Missing before the first poll.", []string{"tailnet"}, nil)
	newestDesc = prometheus.NewDesc(namespace+"_flow_newest_timestamp_seconds",
		"Unix time of the start of the newest stored minute of flow data. Missing when the tailnet has none.", []string{"tailnet"}, nil)
	dbSizeDesc = prometheus.NewDesc(namespace+"_db_size_bytes",
		"Size of the SQLite database file plus its WAL and shared-memory files.", nil, nil)
)

// pollerCollector reads poller counters from memory and two single-row
// store reads per tailnet on each scrape. Nothing is added to the poll path.
type pollerCollector struct {
	registry *services.Registry
	store    Store
	errors   prometheus.Counter
}

func (c *pollerCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{pollerUpDesc, lastSuccessDesc, pollErrorsDesc, ingestedDesc,
		lastPollRowsDesc, cursorDesc, newestDesc, dbSizeDesc} {
		ch <- d
	}
}

func (c *pollerCollector) Collect(ch chan<- prometheus.Metric) {
	if c.store != nil {
		ch <- prometheus.MustNewConstMetric(dbSizeDesc, prometheus.GaugeValue, float64(c.store.DiskBytes()))
	}
	if c.registry == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), scrapeQueryTimeout)
	defer cancel()
	for _, entry := range c.registry.List() {
		if entry == nil || entry.Poller == nil {
			continue
		}
		id := entry.ID
		s := entry.Poller.MetricsSnapshot()
		up := 0.0
		if s.Running {
			up = 1
		}
		ch <- prometheus.MustNewConstMetric(pollerUpDesc, prometheus.GaugeValue, up, id, s.FlowBackend)
		ch <- prometheus.MustNewConstMetric(lastSuccessDesc, prometheus.GaugeValue, unixSeconds(s.LastSuccessTime), id)
		ch <- prometheus.MustNewConstMetric(pollErrorsDesc, prometheus.CounterValue, float64(s.PollErrors), id)
		ch <- prometheus.MustNewConstMetric(ingestedDesc, prometheus.CounterValue, float64(s.TotalPolled), id)
		ch <- prometheus.MustNewConstMetric(lastPollRowsDesc, prometheus.GaugeValue, float64(s.LastPollCount), id)
		if c.store == nil {
			continue
		}
		if cursor, err := c.store.GetPollStateTime(ctx, id); err != nil {
			c.errors.Inc()
		} else if !cursor.IsZero() {
			ch <- prometheus.MustNewConstMetric(cursorDesc, prometheus.GaugeValue, unixSeconds(cursor), id)
		}
		if newest, ok, err := c.store.NewestPairBucket(ctx, id); err != nil {
			c.errors.Inc()
		} else if ok {
			ch <- prometheus.MustNewConstMetric(newestDesc, prometheus.GaugeValue, unixSeconds(newest), id)
		}
	}
}

func unixSeconds(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.UnixNano()) / 1e9
}
