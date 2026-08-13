package services

import (
	"log"
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

	// Process virtual traffic
	for _, traffic := range tsLog.VirtualTraffic {
		srcIP, dstIP, ok := flowEndpoints(traffic.Src, traffic.Dst)
		if !ok {
			continue
		}
		flowLogs = append(flowLogs, database.FlowLog{
			LoggedAt:    logTime,
			NodeID:      tsLog.NodeID,
			TrafficType: "virtual",
			Protocol:    traffic.Proto,
			SrcIP:       srcIP,
			SrcPort:     extractPort(traffic.Src),
			DstIP:       dstIP,
			DstPort:     extractPort(traffic.Dst),
			TxBytes:     int64(traffic.TxBytes),
			RxBytes:     int64(traffic.RxBytes),
			TxPkts:      int64(traffic.TxPkts),
			RxPkts:      int64(traffic.RxPkts),
		})
	}

	// Process subnet traffic
	for _, traffic := range tsLog.SubnetTraffic {
		srcIP, dstIP, ok := flowEndpoints(traffic.Src, traffic.Dst)
		if !ok {
			continue
		}
		flowLogs = append(flowLogs, database.FlowLog{
			LoggedAt:    logTime,
			NodeID:      tsLog.NodeID,
			TrafficType: "subnet",
			Protocol:    traffic.Proto,
			SrcIP:       srcIP,
			SrcPort:     extractPort(traffic.Src),
			DstIP:       dstIP,
			DstPort:     extractPort(traffic.Dst),
			TxBytes:     int64(traffic.TxBytes),
			RxBytes:     int64(traffic.RxBytes),
			TxPkts:      int64(traffic.TxPkts),
			RxPkts:      int64(traffic.RxPkts),
		})
	}

	// Process exit traffic (traffic via exit nodes)
	for _, traffic := range tsLog.ExitTraffic {
		srcIP, dstIP, ok := flowEndpoints(traffic.Src, traffic.Dst)
		if !ok {
			continue
		}
		flowLogs = append(flowLogs, database.FlowLog{
			LoggedAt:    logTime,
			NodeID:      tsLog.NodeID,
			TrafficType: "exit",
			Protocol:    traffic.Proto,
			SrcIP:       srcIP,
			SrcPort:     extractPort(traffic.Src),
			DstIP:       dstIP,
			DstPort:     extractPort(traffic.Dst),
			TxBytes:     int64(traffic.TxBytes),
			RxBytes:     int64(traffic.RxBytes),
			TxPkts:      int64(traffic.TxPkts),
			RxPkts:      int64(traffic.RxPkts),
		})
	}

	// Process physical traffic
	for _, traffic := range tsLog.PhysicalTraffic {
		srcIP, dstIP, ok := flowEndpoints(traffic.Src, traffic.Dst)
		if !ok {
			continue
		}
		flowLogs = append(flowLogs, database.FlowLog{
			LoggedAt:    logTime,
			NodeID:      tsLog.NodeID,
			TrafficType: "physical",
			Protocol:    traffic.Proto,
			SrcIP:       srcIP,
			SrcPort:     extractPort(traffic.Src),
			DstIP:       dstIP,
			DstPort:     extractPort(traffic.Dst),
			TxBytes:     int64(traffic.TxBytes),
			RxBytes:     0,
			TxPkts:      int64(traffic.TxPkts),
			RxPkts:      0,
		})
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
					rxBytes := getInt64(tMap, "rxBytes")
					rxPkts := getInt64(tMap, "rxPkts")
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
						TxBytes:     getInt64(tMap, "txBytes"),
						RxBytes:     rxBytes,
						TxPkts:      getInt64(tMap, "txPkts"),
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
	if v, ok := m[key].(float64); ok {
		// Validate float is within int range and not NaN/Inf
		if v != v || v > float64(int(^uint(0)>>1)) || v < float64(-int(^uint(0)>>1)-1) {
			return 0
		}
		return int(v)
	}
	return 0
}

func getInt64(m map[string]any, key string) int64 {
	if v, ok := m[key].(float64); ok {
		// Validate float is within int64 range and not NaN/Inf
		// Note: float64 can't exactly represent all int64 values, but this catches major issues
		if v != v || v > float64(1<<63-1) || v < float64(-1<<63) {
			return 0
		}
		return int64(v)
	}
	return 0
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
