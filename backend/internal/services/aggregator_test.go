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

func TestAggregatePreservesDirectionalProtocolAndPortOrientation(t *testing.T) {
	poller := NewPoller(nil, nil, DefaultPollerConfig())
	base := time.Now().UTC().Truncate(time.Minute)
	logs := []database.FlowLog{
		{LoggedAt: base.Add(5 * time.Second), SrcIP: "100.64.0.1", DstIP: "100.64.0.2", TrafficType: "virtual", Protocol: 6, DstPort: 443, TxBytes: 100},
		{LoggedAt: base.Add(10 * time.Second), SrcIP: "100.64.0.2", DstIP: "100.64.0.1", TrafficType: "virtual", Protocol: 17, DstPort: 53, TxBytes: 40},
	}

	pairs, _, _, _ := poller.aggregate(logs)
	if len(pairs) != 1 {
		t.Fatalf("expected one normalized pair, got %d", len(pairs))
	}
	pair := pairs[0]
	if pair.SrcNodeID != "100.64.0.1" || pair.DstNodeID != "100.64.0.2" {
		t.Fatalf("normalized pair = %s -> %s", pair.SrcNodeID, pair.DstNodeID)
	}
	if pair.TxBytes != 100 || pair.RxBytes != 40 || !pair.DirectionalPorts {
		t.Fatalf("directional totals = %+v", pair)
	}

	var txProtocols, rxProtocols map[string]int64
	if err := json.Unmarshal([]byte(pair.TxProtocolBytes), &txProtocols); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(pair.RxProtocolBytes), &rxProtocols); err != nil {
		t.Fatal(err)
	}
	if txProtocols["6"] != 100 || len(txProtocols) != 1 {
		t.Fatalf("forward protocols = %+v", txProtocols)
	}
	if rxProtocols["17"] != 40 || len(rxProtocols) != 1 {
		t.Fatalf("reverse protocols = %+v", rxProtocols)
	}

	var txPorts, rxPorts []database.PortStat
	if err := json.Unmarshal([]byte(pair.TxPorts), &txPorts); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(pair.RxPorts), &rxPorts); err != nil {
		t.Fatal(err)
	}
	if len(txPorts) != 1 || txPorts[0].Port != 443 || txPorts[0].Proto != 6 {
		t.Fatalf("forward ports = %+v", txPorts)
	}
	if len(rxPorts) != 1 || rxPorts[0].Port != 53 || rxPorts[0].Proto != 17 {
		t.Fatalf("reverse ports = %+v", rxPorts)
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

func TestAggregateSelfTrafficDoesNotDoubleCountPerNodeBandwidth(t *testing.T) {
	poller := NewPoller(nil, nil, DefaultPollerConfig())
	base := time.Now().UTC().Truncate(time.Minute)
	logs := []database.FlowLog{
		{LoggedAt: base.Add(5 * time.Second), SrcIP: "100.64.0.1", DstIP: "100.64.0.2", TrafficType: "virtual", Protocol: 6, TxBytes: 100},
		{LoggedAt: base.Add(10 * time.Second), SrcIP: "100.64.0.2", DstIP: "100.64.0.1", TrafficType: "virtual", Protocol: 6, TxBytes: 40},
		{LoggedAt: base.Add(15 * time.Second), SrcIP: "100.64.0.3", DstIP: "100.64.0.3", TrafficType: "virtual", Protocol: 6, TxBytes: 7},
	}

	pairs, bandwidth, nodeBandwidth, stats := poller.aggregate(logs)
	if len(pairs) != 2 || len(bandwidth) != 1 || len(nodeBandwidth) != 3 || len(stats) != 1 {
		t.Fatalf("aggregate sizes = pairs %d, bandwidth %d, node bandwidth %d, stats %d; want 2, 1, 3, 1", len(pairs), len(bandwidth), len(nodeBandwidth), len(stats))
	}

	if bandwidth[0].TxBytes != 147 || bandwidth[0].RxBytes != 0 {
		t.Fatalf("total bandwidth = %+v, want TX 147 and RX 0", bandwidth[0])
	}
	if stats[0].TCPBytes != 147 || stats[0].TotalFlows != 3 || stats[0].UniquePairs != 2 {
		t.Fatalf("traffic stats = %+v, want TCP 147, three flows, two pairs", stats[0])
	}

	nodeTotals := make(map[string]database.NodeBandwidth, len(nodeBandwidth))
	for _, node := range nodeBandwidth {
		nodeTotals[node.NodeID] = node
	}
	if got := nodeTotals["100.64.0.1"]; got.TxBytes != 100 || got.RxBytes != 40 {
		t.Fatalf("node 100.64.0.1 bandwidth = %+v, want TX 100 RX 40", got)
	}
	if got := nodeTotals["100.64.0.2"]; got.TxBytes != 40 || got.RxBytes != 100 {
		t.Fatalf("node 100.64.0.2 bandwidth = %+v, want TX 40 RX 100", got)
	}
	if got := nodeTotals["100.64.0.3"]; got.TxBytes != 7 || got.RxBytes != 0 {
		t.Fatalf("self node bandwidth = %+v, want TX 7 RX 0", got)
	}

	var selfPair *database.NodePairAggregate
	for i := range pairs {
		if pairs[i].SrcNodeID == "100.64.0.3" {
			selfPair = &pairs[i]
			break
		}
	}
	if selfPair == nil || selfPair.TxBytes != 7 || selfPair.RxBytes != 0 || selfPair.FlowCount != 1 {
		t.Fatalf("self pair = %+v, want TX 7 RX 0 and one flow", selfPair)
	}
}

func TestAggregateTrafficStatsDistinguishDelimiterContainingPairs(t *testing.T) {
	poller := NewPoller(nil, nil, DefaultPollerConfig())
	base := time.Now().UTC().Truncate(time.Minute)
	logs := []database.FlowLog{
		{LoggedAt: base.Add(5 * time.Second), SrcIP: "a|b", DstIP: "c", TrafficType: "virtual", Protocol: 6, TxBytes: 100},
		{LoggedAt: base.Add(10 * time.Second), SrcIP: "a", DstIP: "b|c", TrafficType: "virtual", Protocol: 17, TxBytes: 200},
	}

	pairs, _, _, stats := poller.aggregate(logs)
	if len(pairs) != 2 || len(stats) != 1 {
		t.Fatalf("aggregate sizes = pairs %d, stats %d; want 2 and 1", len(pairs), len(stats))
	}
	if stats[0].TCPBytes != 100 || stats[0].UDPBytes != 200 || stats[0].TotalFlows != 2 || stats[0].UniquePairs != 2 {
		t.Fatalf("traffic stats = %+v, want separate delimiter-containing pairs", stats[0])
	}
}
