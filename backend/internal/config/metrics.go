package config

import (
	"errors"
	"net"
	"strconv"
)

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

func (c *Config) validateMetrics() error {
	if !c.MetricsEnabled() {
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
