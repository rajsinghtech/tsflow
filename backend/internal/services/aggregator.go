package services

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

// aggregate creates pre-computed aggregates from flow logs
// Handles deduplication: Tailscale logs the same traffic from both endpoints
// (A->B logged by A, B->A logged by B). We normalize pairs by sorting node IDs
// and use TX-only for bandwidth since RX duplicates the reverse direction's TX.
func (p *Poller) aggregate(logs []database.FlowLog) (
	[]database.NodePairAggregate,
	[]database.BandwidthBucket,
	[]database.NodeBandwidth,
	[]database.TrafficStats,
) {
	// Node pair aggregation: group by (bucket, nodeA, nodeB, trafficType)
	// where nodeA < nodeB (normalized ordering)
	type nodePairKey struct {
		bucket      int64
		nodeA       string // always the lexicographically smaller ID
		nodeB       string // always the lexicographically larger ID
		trafficType string
	}
	nodePairMap := make(map[nodePairKey]*database.NodePairAggregate)

	// Protocol/port tracking per node pair
	type protoPortKey struct {
		proto int
		port  int
	}
	type pairProtoData struct {
		protocols map[int]int64          // proto number -> total bytes
		ports     map[protoPortKey]int64 // (proto, port) -> total bytes
	}
	pairProtoMap := make(map[nodePairKey]*pairProtoData)

	// Network-wide traffic stats accumulator per bucket
	type trafficStatsAccum struct {
		tcpBytes        int64
		udpBytes        int64
		otherProtoBytes int64
		virtualBytes    int64
		exitBytes       int64
		subnetBytes     int64
		physicalBytes   int64
		totalFlows      int64
		uniquePairs     map[string]struct{} // "nodeA|nodeB" -> exists
		ports           map[protoPortKey]int64
	}
	trafficStatsMap := make(map[int64]*trafficStatsAccum)

	// Total bandwidth: group by bucket (TX-only to avoid double counting)
	bandwidthMap := make(map[int64]*database.BandwidthBucket)

	// Per-node bandwidth: group by (bucket, nodeID)
	type nodeBwKey struct {
		bucket int64
		nodeID string
	}
	nodeBwMap := make(map[nodeBwKey]*database.NodeBandwidth)

	bucketSize := int64(60) // 1-minute buckets

	for _, log := range logs {
		// Calculate bucket
		bucket := (log.LoggedAt.Unix() / bucketSize) * bucketSize

		// Resolve IPs to device IDs
		srcNodeID := p.deviceCache.ResolveIP(log.SrcIP)
		dstNodeID := p.deviceCache.ResolveIP(log.DstIP)

		// Normalize node pair: smaller ID is always nodeA
		// This merges A->B and B->A into a single bidirectional record
		var nodeA, nodeB string
		var isReverse bool
		if srcNodeID == dstNodeID {
			// Self-traffic: device talking to itself. Keep TX as TX (no swap).
			nodeA, nodeB = srcNodeID, dstNodeID
			isReverse = false
		} else if srcNodeID < dstNodeID {
			nodeA, nodeB = srcNodeID, dstNodeID
			isReverse = false
		} else {
			nodeA, nodeB = dstNodeID, srcNodeID
			isReverse = true
		}

		// Node pair aggregate with normalized key
		npKey := nodePairKey{
			bucket:      bucket,
			nodeA:       nodeA,
			nodeB:       nodeB,
			trafficType: log.TrafficType,
		}

		if agg, ok := nodePairMap[npKey]; ok {
			if isReverse {
				// This log is B->A, so TX goes to RxBytes (traffic from B to A)
				agg.RxBytes += log.TxBytes
				agg.RxPkts += log.TxPkts
			} else {
				// This log is A->B, so TX goes to TxBytes (traffic from A to B)
				agg.TxBytes += log.TxBytes
				agg.TxPkts += log.TxPkts
			}
			agg.FlowCount++
		} else {
			agg := &database.NodePairAggregate{
				Bucket:        bucket,
				SrcNodeID:     nodeA, // nodeA is always "src" in normalized form
				DstNodeID:     nodeB, // nodeB is always "dst" in normalized form
				TrafficType:   log.TrafficType,
				FlowCount:     1,
				Protocols:     "[]",
				ProtocolBytes: "{}",
				Ports:         "[]",
			}
			if isReverse {
				agg.RxBytes = log.TxBytes
				agg.RxPkts = log.TxPkts
			} else {
				agg.TxBytes = log.TxBytes
				agg.TxPkts = log.TxPkts
			}
			nodePairMap[npKey] = agg
		}

		// Track protocols/ports for this node pair
		ppData, ok := pairProtoMap[npKey]
		if !ok {
			ppData = &pairProtoData{
				protocols: make(map[int]int64),
				ports:     make(map[protoPortKey]int64),
			}
			pairProtoMap[npKey] = ppData
		}
		ppData.protocols[log.Protocol] += log.TxBytes
		if log.DstPort > 0 {
			ppData.ports[protoPortKey{proto: log.Protocol, port: log.DstPort}] += log.TxBytes
		}

		// Accumulate network-wide traffic stats
		tsAccum, ok := trafficStatsMap[bucket]
		if !ok {
			tsAccum = &trafficStatsAccum{
				uniquePairs: make(map[string]struct{}),
				ports:       make(map[protoPortKey]int64),
			}
			trafficStatsMap[bucket] = tsAccum
		}
		switch log.Protocol {
		case 6:
			tsAccum.tcpBytes += log.TxBytes
		case 17:
			tsAccum.udpBytes += log.TxBytes
		default:
			tsAccum.otherProtoBytes += log.TxBytes
		}
		switch log.TrafficType {
		case "virtual":
			tsAccum.virtualBytes += log.TxBytes
		case "exit":
			tsAccum.exitBytes += log.TxBytes
		case "subnet":
			tsAccum.subnetBytes += log.TxBytes
		case "physical":
			tsAccum.physicalBytes += log.TxBytes
		}
		tsAccum.totalFlows++
		tsAccum.uniquePairs[nodeA+"|"+nodeB] = struct{}{}
		if log.DstPort > 0 {
			tsAccum.ports[protoPortKey{proto: log.Protocol, port: log.DstPort}] += log.TxBytes
		}

		// Total bandwidth: TX-only (avoids double counting)
		// Each byte is transmitted once, so sum of all TX = total traffic
		if bw, ok := bandwidthMap[bucket]; ok {
			bw.TxBytes += log.TxBytes
		} else {
			bandwidthMap[bucket] = &database.BandwidthBucket{
				Time:    time.Unix(bucket, 0).UTC(),
				TxBytes: log.TxBytes,
				RxBytes: 0, // Not used - would duplicate TX
			}
		}

		// Per-node bandwidth: TX-only approach
		// srcNode's TX = what it sent
		// dstNode's RX = what it received = srcNode's TX
		srcBwKey := nodeBwKey{bucket: bucket, nodeID: srcNodeID}
		if bw, ok := nodeBwMap[srcBwKey]; ok {
			bw.TxBytes += log.TxBytes
		} else {
			nodeBwMap[srcBwKey] = &database.NodeBandwidth{
				Bucket:  bucket,
				NodeID:  srcNodeID,
				TxBytes: log.TxBytes,
				RxBytes: 0,
			}
		}

		// A self-flow has one endpoint, so attributing it as both TX and RX would
		// count the same transmitted bytes twice in per-node totals.
		if srcNodeID != dstNodeID {
			// dstNode receives what srcNode transmitted
			dstBwKey := nodeBwKey{bucket: bucket, nodeID: dstNodeID}
			if bw, ok := nodeBwMap[dstBwKey]; ok {
				bw.RxBytes += log.TxBytes // dst receives what src sent
			} else {
				nodeBwMap[dstBwKey] = &database.NodeBandwidth{
					Bucket:  bucket,
					NodeID:  dstNodeID,
					TxBytes: 0,
					RxBytes: log.TxBytes, // dst receives what src sent
				}
			}
		}
	}

	// Serialize protocol/port data into node pair aggregates
	for key, agg := range nodePairMap {
		if ppData, ok := pairProtoMap[key]; ok {
			// Protocols: most-byte-heavy first, with protocol number as a
			// deterministic tie-breaker. Consumers use the first item as the
			// dominant protocol.
			protos := make([]int, 0, len(ppData.protocols))
			for p := range ppData.protocols {
				protos = append(protos, p)
			}
			sort.Slice(protos, func(i, j int) bool {
				left, right := ppData.protocols[protos[i]], ppData.protocols[protos[j]]
				if left != right {
					return left > right
				}
				return protos[i] < protos[j]
			})
			if b, err := json.Marshal(protos); err == nil {
				agg.Protocols = string(b)
			}
			if b, err := json.Marshal(ppData.protocols); err == nil {
				agg.ProtocolBytes = string(b)
			}

			// Ports: top 20 by bytes as [{port, proto, bytes}, ...]
			type portEntry struct {
				Port  int   `json:"port"`
				Proto int   `json:"proto"`
				Bytes int64 `json:"bytes"`
			}
			portEntries := make([]portEntry, 0, len(ppData.ports))
			for ppk, bytes := range ppData.ports {
				portEntries = append(portEntries, portEntry{Port: ppk.port, Proto: ppk.proto, Bytes: bytes})
			}
			sort.Slice(portEntries, func(i, j int) bool {
				if portEntries[i].Bytes != portEntries[j].Bytes {
					return portEntries[i].Bytes > portEntries[j].Bytes
				}
				if portEntries[i].Proto != portEntries[j].Proto {
					return portEntries[i].Proto < portEntries[j].Proto
				}
				return portEntries[i].Port < portEntries[j].Port
			})
			if len(portEntries) > 20 {
				portEntries = portEntries[:20]
			}
			if b, err := json.Marshal(portEntries); err == nil {
				agg.Ports = string(b)
			}
		}
	}

	// Build TrafficStats from accumulated data
	trafficStats := make([]database.TrafficStats, 0, len(trafficStatsMap))
	for bucket, accum := range trafficStatsMap {
		// Top 20 ports by bytes
		type portEntry struct {
			Port  int   `json:"port"`
			Proto int   `json:"proto"`
			Bytes int64 `json:"bytes"`
		}
		portEntries := make([]portEntry, 0, len(accum.ports))
		for ppk, bytes := range accum.ports {
			portEntries = append(portEntries, portEntry{Port: ppk.port, Proto: ppk.proto, Bytes: bytes})
		}
		sort.Slice(portEntries, func(i, j int) bool {
			if portEntries[i].Bytes != portEntries[j].Bytes {
				return portEntries[i].Bytes > portEntries[j].Bytes
			}
			if portEntries[i].Proto != portEntries[j].Proto {
				return portEntries[i].Proto < portEntries[j].Proto
			}
			return portEntries[i].Port < portEntries[j].Port
		})
		if len(portEntries) > 20 {
			portEntries = portEntries[:20]
		}
		topPortsJSON := "[]"
		if b, err := json.Marshal(portEntries); err == nil {
			topPortsJSON = string(b)
		}

		trafficStats = append(trafficStats, database.TrafficStats{
			Bucket:          bucket,
			TCPBytes:        accum.tcpBytes,
			UDPBytes:        accum.udpBytes,
			OtherProtoBytes: accum.otherProtoBytes,
			VirtualBytes:    accum.virtualBytes,
			ExitBytes:       accum.exitBytes,
			SubnetBytes:     accum.subnetBytes,
			PhysicalBytes:   accum.physicalBytes,
			TotalFlows:      accum.totalFlows,
			UniquePairs:     int64(len(accum.uniquePairs)),
			TopPorts:        topPortsJSON,
		})
	}

	// Convert maps to slices
	nodePairs := make([]database.NodePairAggregate, 0, len(nodePairMap))
	for _, agg := range nodePairMap {
		nodePairs = append(nodePairs, *agg)
	}
	sort.Slice(nodePairs, func(i, j int) bool {
		if nodePairs[i].Bucket != nodePairs[j].Bucket {
			return nodePairs[i].Bucket < nodePairs[j].Bucket
		}
		if nodePairs[i].SrcNodeID != nodePairs[j].SrcNodeID {
			return nodePairs[i].SrcNodeID < nodePairs[j].SrcNodeID
		}
		if nodePairs[i].DstNodeID != nodePairs[j].DstNodeID {
			return nodePairs[i].DstNodeID < nodePairs[j].DstNodeID
		}
		return nodePairs[i].TrafficType < nodePairs[j].TrafficType
	})

	totalBandwidth := make([]database.BandwidthBucket, 0, len(bandwidthMap))
	for _, bw := range bandwidthMap {
		totalBandwidth = append(totalBandwidth, *bw)
	}
	sort.Slice(totalBandwidth, func(i, j int) bool {
		return totalBandwidth[i].Time.Before(totalBandwidth[j].Time)
	})

	nodeBandwidth := make([]database.NodeBandwidth, 0, len(nodeBwMap))
	for _, bw := range nodeBwMap {
		nodeBandwidth = append(nodeBandwidth, *bw)
	}
	sort.Slice(nodeBandwidth, func(i, j int) bool {
		if nodeBandwidth[i].Bucket != nodeBandwidth[j].Bucket {
			return nodeBandwidth[i].Bucket < nodeBandwidth[j].Bucket
		}
		return nodeBandwidth[i].NodeID < nodeBandwidth[j].NodeID
	})
	sort.Slice(trafficStats, func(i, j int) bool {
		return trafficStats[i].Bucket < trafficStats[j].Bucket
	})

	return nodePairs, totalBandwidth, nodeBandwidth, trafficStats
}
