package config

import (
	"errors"
	"fmt"
	"net"
	"strconv"
)

// MaxMetricsTopTalkers caps TSFLOW_METRICS_TOP_TALKERS. Each talker is a few
// series, so the cap keeps a typo from adding thousands.
const MaxMetricsTopTalkers = 100

// MetricsEnabled reports whether TSFLOW_METRICS_ADDR is set.
func (c *Config) MetricsEnabled() bool {
	return c != nil && c.MetricsAddr != ""
}

// MetricsListenAddr is TSFLOW_METRICS_ADDR as a listen address. A bare port
// such as 9090 listens on every interface, the same as :9090.
func (c *Config) MetricsListenAddr() string {
	if c == nil || c.MetricsAddr == "" {
		return ""
	}
	if _, err := strconv.Atoi(c.MetricsAddr); err == nil {
		return ":" + c.MetricsAddr
	}
	return c.MetricsAddr
}

// MetricsSharesMainPort reports whether the metrics address is the UI port
// on every interface. tsflow then serves /metrics on the main listener
// instead of failing to bind the port twice. tsnet mode never does this,
// because its UI is not on a plain port.
func (c *Config) MetricsSharesMainPort() bool {
	if !c.MetricsEnabled() || c.TsnetServe {
		return false
	}
	host, port, err := net.SplitHostPort(c.MetricsListenAddr())
	if err != nil {
		return false
	}
	return port == c.Port && (host == "" || host == "0.0.0.0")
}

// MetricsTopTalkerCount is TSFLOW_METRICS_TOP_TALKERS as a number. Empty
// and invalid values are 0; Validate rejects the invalid ones.
func (c *Config) MetricsTopTalkerCount() int {
	if c == nil || c.MetricsTopTalkers == "" {
		return 0
	}
	n, err := strconv.Atoi(c.MetricsTopTalkers)
	if err != nil || n < 0 {
		return 0
	}
	return min(n, MaxMetricsTopTalkers)
}

func (c *Config) validateMetrics() error {
	if c.MetricsTopTalkers != "" {
		n, err := strconv.Atoi(c.MetricsTopTalkers)
		if err != nil || n < 0 || n > MaxMetricsTopTalkers {
			return fmt.Errorf("TSFLOW_METRICS_TOP_TALKERS must be a number from 0 to %d", MaxMetricsTopTalkers)
		}
	}
	if !c.MetricsEnabled() {
		if c.MetricsTraffic || c.MetricsTopTalkerCount() > 0 {
			return errors.New("TSFLOW_METRICS_TRAFFIC and TSFLOW_METRICS_TOP_TALKERS need TSFLOW_METRICS_ADDR")
		}
		return nil
	}
	_, port, err := net.SplitHostPort(c.MetricsListenAddr())
	if err != nil {
		return errors.New("TSFLOW_METRICS_ADDR must be a port or host:port, for example :9090")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return errors.New("TSFLOW_METRICS_ADDR port must be a number between 1 and 65535")
	}
	if c.TsnetServe && c.TsnetHealthPort == port {
		return errors.New("TSFLOW_METRICS_ADDR and TSFLOW_HEALTH_PORT must use different ports")
	}
	return nil
}
