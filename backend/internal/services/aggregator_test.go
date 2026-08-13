package services

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

func TestAggregateOrdersProtocolsByBytesAndPortsDeterministically(t *testing.T) {
	poller := NewPoller(nil, nil, DefaultPollerConfig())
	base := time.Now().UTC().Truncate(time.Minute)
	logs := []database.FlowLog{
		{LoggedAt: base.Add(5 * time.Second), SrcIP: "100.64.0.1", DstIP: "100.64.0.2", TrafficType: "virtual", Protocol: 6, DstPort: 443, TxBytes: 100},
		{LoggedAt: base.Add(10 * time.Second), SrcIP: "100.64.0.1", DstIP: "100.64.0.2", TrafficType: "virtual", Protocol: 17, DstPort: 53, TxBytes: 300},
		{LoggedAt: base.Add(15 * time.Second), SrcIP: "100.64.0.1", DstIP: "100.64.0.2", TrafficType: "virtual", Protocol: 6, DstPort: 80, TxBytes: 200},
	}

	nodePairs, _, _, trafficStats := poller.aggregate(logs)
	if len(nodePairs) != 1 || len(trafficStats) != 1 {
		t.Fatalf("expected one pair and one stats bucket, got %d and %d", len(nodePairs), len(trafficStats))
	}

	var protocols []int
	if err := json.Unmarshal([]byte(nodePairs[0].Protocols), &protocols); err != nil {
		t.Fatal(err)
	}
	if len(protocols) != 2 || protocols[0] != 6 || protocols[1] != 17 {
		t.Fatalf("protocol order = %v, want [6 17]", protocols)
	}
	var protocolBytes map[string]int64
	if err := json.Unmarshal([]byte(nodePairs[0].ProtocolBytes), &protocolBytes); err != nil {
		t.Fatal(err)
	}
	if protocolBytes["6"] != 300 || protocolBytes["17"] != 300 {
		t.Fatalf("protocol byte totals = %v", protocolBytes)
	}

	var ports []database.PortStat
	if err := json.Unmarshal([]byte(nodePairs[0].Ports), &ports); err != nil {
		t.Fatal(err)
	}
	if len(ports) != 3 || ports[0].Port != 53 || ports[1].Port != 80 || ports[2].Port != 443 {
		t.Fatalf("ports = %+v", ports)
	}
}

func TestAggregateCapsTopPortsAtTwenty(t *testing.T) {
	poller := NewPoller(nil, nil, DefaultPollerConfig())
	base := time.Now().UTC().Truncate(time.Minute)
	logs := make([]database.FlowLog, 0, 25)
	for port := 1; port <= 25; port++ {
		logs = append(logs, database.FlowLog{
			LoggedAt: base.Add(time.Duration(port) * time.Second), SrcIP: "100.64.0.1", DstIP: "100.64.0.2",
			TrafficType: "virtual", Protocol: 6, DstPort: port, TxBytes: int64(port),
		})
	}
	nodePairs, _, _, trafficStats := poller.aggregate(logs)
	if len(nodePairs) != 1 || len(trafficStats) != 1 {
		t.Fatalf("expected one pair and stats bucket")
	}
	for _, raw := range []string{nodePairs[0].Ports, trafficStats[0].TopPorts} {
		var ports []database.PortStat
		if err := json.Unmarshal([]byte(raw), &ports); err != nil {
			t.Fatal(err)
		}
		if len(ports) != 20 {
			t.Fatalf("expected 20 ports, got %d", len(ports))
		}
		if ports[0].Port != 25 || ports[19].Port != 6 {
			t.Fatalf("ports not deterministically ranked: first=%+v last=%+v", ports[0], ports[19])
		}
	}
}
