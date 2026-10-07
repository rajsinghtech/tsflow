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
	// ProtocolBytes preserves the byte totals needed to identify the dominant
	// protocol after multiple polls are merged. It is kept internal to the
	// aggregate response; callers receive the derived Protocol field instead.
	ProtocolBytes string `json:"-"`
	Ports         string `json:"ports"` // JSON array of top ports
	// TxPorts and RxPorts preserve destination-port observations for each
	// direction of the normalized pair. They are internal storage fields; the
	// aggregated-flow handler exposes them only when DirectionalPorts is true.
	TxPorts         string `json:"-"`
	RxPorts         string `json:"-"`
	TxProtocolBytes string `json:"-"`
	RxProtocolBytes string `json:"-"`
	// DirectionalPorts indicates that all metadata contributing to this
	// aggregate has direction-preserving protocol/port information. It is
	// intentionally false for legacy rows so callers can use the old union
	// metadata without attributing it to the wrong direction.
	DirectionalPorts bool `json:"-"`
}

// BandwidthBucket represents aggregated bandwidth for a time bucket
type BandwidthBucket struct {
	Time    time.Time `json:"time"`
	TxBytes int64     `json:"txBytes"`
	RxBytes int64     `json:"rxBytes"`
	// Seconds is the portion of this bucket covered by the query window.
	// Edge buckets are often shorter than the nominal bucket size. Zero means
	// the caller has not annotated coverage yet.
	Seconds int64 `json:"seconds,omitempty"`
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
	ExitBytes       int64  `json:"exitBytes"`
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

const (
	// RankDefaultLimit is the page size for ranked talker and pair reads.
	RankDefaultLimit = 20
	// RankMaxLimit is the largest page a ranked read will return.
	RankMaxLimit = 200
	// RankMaxOffset bounds how far a ranked page can skip.
	RankMaxOffset = 100000
	// RankSortBytes orders by total volume descending.
	RankSortBytes = "bytes"
	// RankSortFlows orders by flow count descending.
	RankSortFlows = "flows"
)

// RankedTalker is one device over a window, with the same byte and flow
// fields the pair and traffic-stats reads already use.
// Owner is set when the read was filtered by tag or login. It is the merged
// login, including a creator login copied onto a tagged device.
type RankedTalker struct {
	NodeID     string `json:"nodeId"`
	Hostname   string `json:"hostname"`
	Owner      string `json:"owner,omitempty"`
	TxBytes    int64  `json:"txBytes"`
	RxBytes    int64  `json:"rxBytes"`
	TotalBytes int64  `json:"totalBytes"`
	FlowCount  int64  `json:"flowCount"`
}

// RankedPair is one directed src/dst over a window.
// SrcOwner and DstOwner are set when the read was filtered by tag or login.
type RankedPair struct {
	SrcNodeID   string `json:"srcNodeId"`
	SrcHostname string `json:"srcHostname"`
	SrcOwner    string `json:"srcOwner,omitempty"`
	DstNodeID   string `json:"dstNodeId"`
	DstHostname string `json:"dstHostname"`
	DstOwner    string `json:"dstOwner,omitempty"`
	TxBytes     int64  `json:"txBytes"`
	RxBytes     int64  `json:"rxBytes"`
	TotalBytes  int64  `json:"totalBytes"`
	FlowCount   int64  `json:"flowCount"`
}

// RankQuery selects one page of a ranked read.
// An empty Sort means bytes. Limit <= 0 selects RankDefaultLimit.
// Tag, User, and Q are empty for an unfiltered read. Tag and User are both
// required when both are set. Q matches a login, tag, hostname, name, or address.
type RankQuery struct {
	Limit        int
	Offset       int
	Sort         string
	TrafficTypes []string
	Tag          string
	User         string
	Q            string
	// ExactUser requires the full login. The me scope sets this from the
	// viewer identity. A client-supplied user filter stays a substring.
	ExactUser bool
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

// ObjectIngestResult records aggregates produced by one immutable object-store log object.
type ObjectIngestResult struct {
	Key           string
	LastModified  time.Time
	Size          int64
	FlowCount     int
	NodeMetadata  []NodeMetadata
	NodePairs     []NodePairAggregate
	Bandwidth     []BandwidthBucket
	NodeBandwidth []NodeBandwidth
	TrafficStats  []TrafficStats
	PollEnd       time.Time
}

// NodeMetadata stores node identities embedded in exported flow-log objects.
// A flow log may identify a node by its stable nodeId or by the legacy numeric
// id. The device cache merges that record into the API device when they are
// the same node.
type NodeMetadata struct {
	NodeID   string    `json:"nodeId"`
	Name     string    `json:"name"`
	Hostname string    `json:"hostname"`
	Owner    string    `json:"owner"`
	IPs      []string  `json:"ips"`
	Tags     []string  `json:"tags"`
	Updated  time.Time `json:"updated"`
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

// Store defines the interface for flow log storage.
// Data methods take a tailnet id and never read or write another tailnet's rows.
type Store interface {
	Init(ctx context.Context) error
	Close() error

	// Pre-aggregated data operations
	UpsertNodePairAggregates(ctx context.Context, tailnetID string, aggregates []NodePairAggregate) error
	GetNodePairAggregates(ctx context.Context, tailnetID string, start, end time.Time) ([]NodePairAggregate, error)

	// Bandwidth operations
	UpsertBandwidth(ctx context.Context, tailnetID string, buckets []BandwidthBucket) error
	UpsertNodeBandwidth(ctx context.Context, tailnetID string, buckets []NodeBandwidth) error
	GetBandwidth(ctx context.Context, tailnetID string, start, end time.Time) ([]BandwidthBucket, error)
	GetBandwidthByTrafficTypes(ctx context.Context, tailnetID string, start, end time.Time, trafficTypes []string) ([]BandwidthBucket, error)
	GetNodeBandwidth(ctx context.Context, tailnetID string, start, end time.Time, nodeID string) ([]BandwidthBucket, error)
	GetNodeBandwidthByTrafficTypes(ctx context.Context, tailnetID string, start, end time.Time, nodeID string, trafficTypes []string) ([]BandwidthBucket, error)

	// Traffic stats operations
	UpsertTrafficStats(ctx context.Context, tailnetID string, stats []TrafficStats) error
	GetTrafficStats(ctx context.Context, tailnetID string, start, end time.Time) ([]TrafficStats, error)
	GetTrafficStatsFromNodePairs(ctx context.Context, tailnetID string, start, end time.Time) ([]TrafficStats, error)
	GetTrafficStatsFromNodePairsByTrafficTypes(ctx context.Context, tailnetID string, start, end time.Time, trafficTypes []string) ([]TrafficStats, error)
	// FillMissingTrafficStats derives stats for aggregated buckets in the
	// window that primary does not already contain. Nil means traffic_stats
	// covers the window and node_pairs was not read.
	FillMissingTrafficStats(ctx context.Context, tailnetID string, start, end time.Time, primary []TrafficStats) ([]TrafficStats, error)
	GetTopTalkers(ctx context.Context, tailnetID string, start, end time.Time, limit int) ([]TopTalker, error)
	GetTopTalkersByTrafficTypes(ctx context.Context, tailnetID string, start, end time.Time, trafficTypes []string, limit int) ([]TopTalker, error)
	// ActiveNodeIDs lists the distinct stored node ids with traffic in the
	// window. A self-pair lists its node once. Ranking limits do not apply.
	// Ids are as stored, so callers resolve aliases before counting devices.
	// An empty trafficTypes list leaves out physical rows.
	ActiveNodeIDs(ctx context.Context, tailnetID string, start, end time.Time, trafficTypes []string) ([]string, error)
	GetTopPairs(ctx context.Context, tailnetID string, start, end time.Time, limit int) ([]TopPair, error)
	GetTopPairsByTrafficTypes(ctx context.Context, tailnetID string, start, end time.Time, trafficTypes []string, limit int) ([]TopPair, error)
	// ListRankedTalkers and ListRankedPairs page device and pair totals.
	// Complete hours come from the hourly rollup. The bool is true when
	// another row exists after this page.
	ListRankedTalkers(ctx context.Context, tailnetID string, start, end time.Time, query RankQuery) ([]RankedTalker, bool, error)
	ListRankedPairs(ctx context.Context, tailnetID string, start, end time.Time, query RankQuery) ([]RankedPair, bool, error)
	// GetDeviceTimeline is one device's bytes over time, split by traffic
	// type, plus one page of peers. Complete hours come from the rollup.
	// An empty traffic type list leaves out physical.
	GetDeviceTimeline(ctx context.Context, tailnetID, nodeID string, start, end time.Time, query TimelineQuery) (*DeviceTimeline, error)
	// ListViewerDevices is every device whose merged login equals login,
	// including a tagged device's creator login. Traffic follows the same
	// physical-exclusion rules as the rankings.
	ListViewerDevices(ctx context.Context, tailnetID, login string, start, end time.Time, trafficTypes []string) ([]ViewerDevice, error)
	// ViewerOwns reports whether nodeID belongs to login in this tailnet.
	ViewerOwns(ctx context.Context, tailnetID, login, nodeID string) (bool, error)
	// ListNewPairs lists src/dst pairs first seen in the window. Complete
	// hours come from the hourly rollup. The bool is true when another row
	// exists after this page. An empty traffic type list leaves out physical.
	ListNewPairs(ctx context.Context, tailnetID string, start, end time.Time, query NewPairQuery) ([]NewPair, bool, error)
	GetNodeStats(ctx context.Context, tailnetID string, nodeID string, start, end time.Time) (*NodeDetailStats, error)
	// DistinctPairs lists the distinct stored src/dst pairs across the whole
	// window. Complete hours come from the hourly rollup. An empty
	// trafficTypes list leaves out physical rows. Ids are as stored, so
	// callers resolve aliases before counting pairs.
	DistinctPairs(ctx context.Context, tailnetID string, start, end time.Time, trafficTypes []string) ([][2]string, error)

	// Atomic poll commit
	CommitPollResults(ctx context.Context, tailnetID string, results PollResults) error
	CommitObjectIngest(ctx context.Context, tailnetID string, result ObjectIngestResult) error
	IsObjectIngested(ctx context.Context, tailnetID string, key string) (bool, error)
	GetObjectsNeedingMetadata(ctx context.Context, tailnetID string, limit int) ([]string, error)
	MarkObjectMetadataHydrated(ctx context.Context, tailnetID string, key string, nodeIDs []string) error
	UpsertNodeMetadata(ctx context.Context, tailnetID string, nodes []NodeMetadata) error
	GetNodeMetadata(ctx context.Context, tailnetID string) ([]NodeMetadata, error)

	// State operations
	GetPollState(ctx context.Context, tailnetID string) (*PollState, error)
	UpdatePollState(ctx context.Context, tailnetID string, lastPollEnd time.Time) error
	GetDataRange(ctx context.Context, tailnetID string) (*DataRange, error)

	// Maintenance
	Cleanup(ctx context.Context, tailnetID string, retention time.Duration) (int64, error)
	GetStats(ctx context.Context, tailnetID string) (map[string]any, error)
}
