package services

import (
	"encoding/json"
	"log"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
	tailscale "tailscale.com/client/tailscale/v2"
)

// convertLogs converts Tailscale API response to database FlowLog entries
func (p *Poller) convertLogs(logsResp any) []database.FlowLog {
	var flowLogs []database.FlowLog

	logsMap, ok := logsResp.(map[string]any)
	if !ok {
		log.Printf("Warning: unexpected logs response type %T, expected map[string]any", logsResp)
		return flowLogs
	}

	logs, ok := logsMap["logs"]
	if !ok {
		log.Printf("Warning: logs response missing 'logs' key, available keys: %v", mapKeys(logsMap))
		return flowLogs
	}

	// Handle []tailscale.NetworkFlowLog
	if tsLogs, ok := logs.([]tailscale.NetworkFlowLog); ok {
		for _, tsLog := range tsLogs {
			flowLogs = append(flowLogs, p.convertTailscaleLog(tsLog)...)
		}
		return flowLogs
	}

	// Handle []any (generic JSON)
	if logsArray, ok := logs.([]any); ok {
		for _, logItem := range logsArray {
			if logMap, ok := logItem.(map[string]any); ok {
				flowLogs = append(flowLogs, p.convertMapLog(logMap)...)
			}
		}
	}

	return flowLogs
}

func (p *Poller) convertTailscaleLog(tsLog tailscale.NetworkFlowLog) []database.FlowLog {
	var flowLogs []database.FlowLog

	// Use Start (when traffic actually occurred) instead of Logged (when server captured it)
	// to avoid 5-10 second timing skew in bucket assignment
	logTime := tsLog.Start
	if logTime.IsZero() {
		logTime = tsLog.Logged // fallback if Start not populated
	}
	if logTime.IsZero() {
		log.Printf("Warning: skipping flow log with no start or logged timestamp for node %s", tsLog.NodeID)
		return flowLogs
	}

	appendTraffic := func(traffic tailscale.TrafficStats, trafficType string, includeRx bool) {
		srcIP, dstIP, ok := flowEndpoints(traffic.Src, traffic.Dst)
		if !ok {
			return
		}
		txBytes, rxBytes, txPkts, rxPkts, ok := convertTypedCounters(traffic, includeRx)
		if !ok {
			log.Printf("Warning: skipping flow log with out-of-range counters for node %s", tsLog.NodeID)
			return
		}
		flowLogs = append(flowLogs, database.FlowLog{
			LoggedAt:    logTime,
			NodeID:      tsLog.NodeID,
			TrafficType: trafficType,
			Protocol:    traffic.Proto,
			SrcIP:       srcIP,
			SrcPort:     extractPort(traffic.Src),
			DstIP:       dstIP,
			DstPort:     extractPort(traffic.Dst),
			TxBytes:     txBytes,
			RxBytes:     rxBytes,
			TxPkts:      txPkts,
			RxPkts:      rxPkts,
		})
	}

	for _, traffic := range tsLog.VirtualTraffic {
		appendTraffic(traffic, "virtual", true)
	}
	for _, traffic := range tsLog.SubnetTraffic {
		appendTraffic(traffic, "subnet", true)
	}
	for _, traffic := range tsLog.ExitTraffic {
		appendTraffic(traffic, "exit", true)
	}
	for _, traffic := range tsLog.PhysicalTraffic {
		appendTraffic(traffic, "physical", false)
	}

	return flowLogs
}

func (p *Poller) convertMapLog(logMap map[string]any) []database.FlowLog {
	var flowLogs []database.FlowLog

	nodeID, ok := logMap["nodeId"].(string)
	if !ok {
		log.Printf("Warning: skipping log entry with invalid nodeId type: %T", logMap["nodeId"])
		return flowLogs
	}
	// Prefer "start" over "logged" for bucket alignment (consistent with convertTailscaleLog)
	logTimeStr := getString(logMap, "start")
	if logTimeStr == "" {
		logTimeStr = getString(logMap, "logged")
	}
	logged, err := time.Parse(time.RFC3339, logTimeStr)
	if err != nil {
		log.Printf("Warning: skipping log entry with invalid timestamp for node %s", nodeID)
		return flowLogs
	}

	// Process each traffic type
	for _, trafficType := range []string{"virtualTraffic", "subnetTraffic", "exitTraffic", "physicalTraffic"} {
		if traffic, ok := logMap[trafficType].([]any); ok {
			typeName := strings.TrimSuffix(trafficType, "Traffic")
			isPhysical := typeName == "physical"
			for _, t := range traffic {
				if tMap, ok := t.(map[string]any); ok {
					srcIP, dstIP, endpointOK := flowEndpoints(getString(tMap, "src"), getString(tMap, "dst"))
					if !endpointOK {
						continue
					}
					txBytes, txBytesOK := getCounter(tMap, "txBytes")
					rxBytes, rxBytesOK := getCounter(tMap, "rxBytes")
					txPkts, txPktsOK := getCounter(tMap, "txPkts")
					rxPkts, rxPktsOK := getCounter(tMap, "rxPkts")
					if !txBytesOK || !txPktsOK || (!isPhysical && (!rxBytesOK || !rxPktsOK)) {
						log.Printf("Warning: skipping flow log with invalid counters for node %s", nodeID)
						continue
					}
					// Physical traffic has no RX data in the Tailscale API
					if isPhysical {
						rxBytes = 0
						rxPkts = 0
					}
					flowLogs = append(flowLogs, database.FlowLog{
						LoggedAt:    logged,
						NodeID:      nodeID,
						TrafficType: typeName,
						Protocol:    getInt(tMap, "proto"),
						SrcIP:       srcIP,
						SrcPort:     extractPort(getString(tMap, "src")),
						DstIP:       dstIP,
						DstPort:     extractPort(getString(tMap, "dst")),
						TxBytes:     txBytes,
						RxBytes:     rxBytes,
						TxPkts:      txPkts,
						RxPkts:      rxPkts,
					})
				}
			}
		}
	}

	return flowLogs
}

// Helper functions
func extractIP(addr string) string {
	host, _, ok := splitEndpoint(addr)
	if ok {
		return host
	}
	return addr
}

func extractPort(addr string) int {
	_, port, ok := splitEndpoint(addr)
	if !ok {
		return 0
	}
	return port
}

func flowEndpoints(src, dst string) (string, string, bool) {
	srcIP := strings.TrimSpace(extractIP(src))
	dstIP := strings.TrimSpace(extractIP(dst))
	return srcIP, dstIP, srcIP != "" && dstIP != ""
}

// splitEndpoint separates an address from an optional port without treating
// an unbracketed IPv6 address as host:port. Tailscale normally emits
// bracketed IPv6 endpoints when a port is present, but exported logs can also
// contain bare IPs and opaque node/host identifiers.
func splitEndpoint(addr string) (host string, port int, ok bool) {
	if addr == "" {
		return "", 0, false
	}

	// A complete IP address is never interpreted as having a port. This is
	// essential for IPv6, where the final colon-separated segment is not a
	// port delimiter unless the address is bracketed.
	if net.ParseIP(addr) != nil {
		return addr, 0, true
	}

	if strings.HasPrefix(addr, "[") {
		end := strings.IndexByte(addr, ']')
		if end <= 1 || net.ParseIP(addr[1:end]) == nil {
			return "", 0, false
		}
		host = addr[1:end]
		suffix := addr[end+1:]
		if suffix == "" {
			return host, 0, true
		}
		if !strings.HasPrefix(suffix, ":") {
			return "", 0, false
		}
		return host, validPort(suffix[1:]), true
	}

	// SplitHostPort handles IPv4 and host names. It rejects bare IPv6, which
	// was already handled above by ParseIP.
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, false
	}
	return host, validPort(portText), true
}

func validPort(raw string) int {
	port, err := strconv.Atoi(raw)
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getInt(m map[string]any, key string) int {
	value, ok := parseInt64(m[key])
	if !ok || value > int64(^uint(0)>>1) || value < -int64(^uint(0)>>1)-1 {
		return 0
	}
	return int(value)
}

func getCounter(m map[string]any, key string) (int64, bool) {
	raw, exists := m[key]
	if !exists || raw == nil {
		return 0, true
	}
	return parseInt64(raw)
}

func parseInt64(raw any) (int64, bool) {
	switch v := raw.(type) {
	case float64:
		// JSON numbers are decoded as float64. Reject fractional, NaN/Inf, and
		// negative values and values at or above 2^63, which float64 rounds
		// MaxInt64 up to. Traffic counters are unsigned in the API.
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) || v < 0 || v >= float64(1<<63) {
			return 0, false
		}
		return int64(v), true
	case float32:
		return parseInt64(float64(v))
	case json.Number:
		value, err := v.Int64()
		return value, err == nil && value >= 0
	case int:
		return int64(v), v >= 0
	case int64:
		return v, v >= 0
	case uint:
		return uint64ToInt64(uint64(v))
	case uint64:
		return uint64ToInt64(v)
	default:
		return 0, false
	}
}

func uint64ToInt64(value uint64) (int64, bool) {
	if value > uint64(^uint64(0)>>1) {
		return 0, false
	}
	return int64(value), true
}

func convertTypedCounters(traffic tailscale.TrafficStats, includeRx bool) (int64, int64, int64, int64, bool) {
	txBytes, ok := uint64ToInt64(traffic.TxBytes)
	if !ok {
		return 0, 0, 0, 0, false
	}
	txPkts, ok := uint64ToInt64(traffic.TxPkts)
	if !ok {
		return 0, 0, 0, 0, false
	}
	if !includeRx {
		return txBytes, 0, txPkts, 0, true
	}
	rxBytes, ok := uint64ToInt64(traffic.RxBytes)
	if !ok {
		return 0, 0, 0, 0, false
	}
	rxPkts, ok := uint64ToInt64(traffic.RxPkts)
	if !ok {
		return 0, 0, 0, 0, false
	}
	return txBytes, rxBytes, txPkts, rxPkts, true
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
