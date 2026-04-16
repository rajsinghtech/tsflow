package database

import (
	"context"
	"time"
)

// NodePairAggregate represents pre-computed node-to-node traffic
// This is the primary data structure for graph rendering
type NodePairAggregate struct {
	Bucket      int64  `json:"bucket"`      // Time bucket (unix timestamp)
	SrcNodeID   string `json:"srcNodeId"`   // Source device ID or IP
	DstNodeID   string `json:"dstNodeId"`   // Destination device ID or IP
	TrafficType string `json:"trafficType"` // virtual, subnet, physical
	TxBytes     int64  `json:"txBytes"`
	RxBytes     int64  `json:"rxBytes"`
	TxPkts      int64  `json:"txPkts"`
	RxPkts      int64  `json:"rxPkts"`
	FlowCount   int64  `json:"flowCount"`
	Protocols   string `json:"protocols"` // JSON array of protocols seen
	Ports       string `json:"ports"`     // JSON array of top ports
}

// BandwidthBucket represents aggregated bandwidth for a time bucket
type BandwidthBucket struct {
	Time    time.Time `json:"time"`
	TxBytes int64     `json:"txBytes"`
	RxBytes int64     `json:"rxBytes"`
}

// NodeBandwidth represents bandwidth for a specific node
type NodeBandwidth struct {
	Bucket  int64  `json:"bucket"`
	NodeID  string `json:"nodeId"`
	TxBytes int64  `json:"txBytes"`
	RxBytes int64  `json:"rxBytes"`
}

// TrafficStats represents network-wide statistics for a time bucket
type TrafficStats struct {
	Bucket          int64  `json:"bucket"`
	TCPBytes        int64  `json:"tcpBytes"`
	UDPBytes        int64  `json:"udpBytes"`
	OtherProtoBytes int64  `json:"otherProtoBytes"`
	VirtualBytes    int64  `json:"virtualBytes"`
	SubnetBytes     int64  `json:"subnetBytes"`
	PhysicalBytes   int64  `json:"physicalBytes"`
	TotalFlows      int64  `json:"totalFlows"`
	UniquePairs     int64  `json:"uniquePairs"`
	TopPorts        string `json:"topPorts"`
}

// TopTalker represents a node ranked by total traffic volume
type TopTalker struct {
	NodeID     string `json:"nodeId"`
	TxBytes    int64  `json:"txBytes"`
	RxBytes    int64  `json:"rxBytes"`
	TotalBytes int64  `json:"totalBytes"`
}

// TopPair represents a node-to-node pair ranked by total traffic volume
type TopPair struct {
	SrcNodeID  string `json:"srcNodeId"`
	DstNodeID  string `json:"dstNodeId"`
	TxBytes    int64  `json:"txBytes"`
	RxBytes    int64  `json:"rxBytes"`
	TotalBytes int64  `json:"totalBytes"`
	FlowCount  int64  `json:"flowCount"`
}

// PortStat represents traffic volume for a specific port/protocol
type PortStat struct {
	Port  int   `json:"port"`
	Proto int   `json:"proto"`
	Bytes int64 `json:"bytes"`
}

// NodeDetailStats represents detailed traffic statistics for a single node
type NodeDetailStats struct {
	NodeID     string     `json:"nodeId"`
	TotalTx    int64      `json:"totalTx"`
	TotalRx    int64      `json:"totalRx"`
	TCPBytes   int64      `json:"tcpBytes"`
	UDPBytes   int64      `json:"udpBytes"`
	OtherBytes int64      `json:"otherBytes"`
	TopPeers   []TopPair  `json:"topPeers"`
	TopPorts   []PortStat `json:"topPorts"`
}

// PollResults bundles all aggregates from a single poll for atomic commit
type PollResults struct {
	NodePairs     []NodePairAggregate
	Bandwidth     []BandwidthBucket
	NodeBandwidth []NodeBandwidth
	TrafficStats  []TrafficStats
	PollEnd       time.Time
}

// PollState tracks the polling state
type PollState struct {
	LastPollEnd time.Time `json:"lastPollEnd"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// DataRange represents the available data time range
type DataRange struct {
	Earliest time.Time `json:"earliest"`
	Latest   time.Time `json:"latest"`
	Count    int64     `json:"count"` // Total records in the range
}

// FlowLog represents a raw flow log entry (kept temporarily for current period)
type FlowLog struct {
	ID          int64     `json:"id"`
	LoggedAt    time.Time `json:"loggedAt"`
	NodeID      string    `json:"nodeId"`
	TrafficType string    `json:"trafficType"`
	Protocol    int       `json:"protocol"`
	SrcIP       string    `json:"srcIp"`
	SrcPort     int       `json:"srcPort"`
	DstIP       string    `json:"dstIp"`
	DstPort     int       `json:"dstPort"`
	TxBytes     int64     `json:"txBytes"`
	RxBytes     int64     `json:"rxBytes"`
	TxPkts      int64     `json:"txPkts"`
	RxPkts      int64     `json:"rxPkts"`
}

// Store defines the interface for flow log storage
type Store interface {
	Init(ctx context.Context) error
	Close() error

	// Pre-aggregated data operations
	UpsertNodePairAggregates(ctx context.Context, aggregates []NodePairAggregate) error
	GetNodePairAggregates(ctx context.Context, start, end time.Time, bucketSize int64) ([]NodePairAggregate, error)

	// Bandwidth operations
	UpsertBandwidth(ctx context.Context, buckets []BandwidthBucket) error
	UpsertNodeBandwidth(ctx context.Context, buckets []NodeBandwidth) error
	GetBandwidth(ctx context.Context, start, end time.Time) ([]BandwidthBucket, error)
	GetNodeBandwidth(ctx context.Context, start, end time.Time, nodeID string) ([]BandwidthBucket, error)

	// Traffic stats operations
	UpsertTrafficStats(ctx context.Context, stats []TrafficStats) error
	GetTrafficStats(ctx context.Context, start, end time.Time) ([]TrafficStats, error)
	GetTrafficStatsFromNodePairs(ctx context.Context, start, end time.Time) ([]TrafficStats, error)
	GetTopTalkers(ctx context.Context, start, end time.Time, limit int) ([]TopTalker, error)
	GetTopPairs(ctx context.Context, start, end time.Time, limit int) ([]TopPair, error)
	GetNodeStats(ctx context.Context, nodeID string, start, end time.Time) (*NodeDetailStats, error)

	// Atomic poll commit
	CommitPollResults(ctx context.Context, results PollResults) error

	// State operations
	GetPollState(ctx context.Context) (*PollState, error)
	UpdatePollState(ctx context.Context, lastPollEnd time.Time) error
	GetDataRange(ctx context.Context) (*DataRange, error)

	// Maintenance
	Cleanup(ctx context.Context, retention time.Duration) (int64, error)
	GetStats(ctx context.Context) (map[string]any, error)
}
