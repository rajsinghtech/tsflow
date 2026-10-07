package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
)

var closedWorld = false

func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:         title,
		ReadOnlyHint:  true,
		OpenWorldHint: &closedWorld,
	}
}

type listTailnetsIn struct{}

type searchDevicesIn struct {
	Tailnet string `json:"tailnet,omitempty" jsonschema:"Tailnet id. Omit when one tailnet is configured, or when the id default is permitted. Required when several tailnets are configured and default is not permitted."`
	Query   string `json:"query,omitempty" jsonschema:"Case-insensitive match against device name, hostname, IP address, tag, or user login. Empty lists visible devices."`
	Scope   string `json:"scope,omitempty" jsonschema:"Device view. mine applies the viewer's identity autoscope and is the default when autoscope is user or groups. all clears that filter and returns every device in the permitted tailnets. A tailnet outside the allowlist is still denied."`
	Limit   int    `json:"limit,omitempty" jsonschema:"Maximum rows. Defaults to 20 and is clamped to 100."`
	Offset  int    `json:"offset,omitempty" jsonschema:"Rows to skip. Defaults to 0 and cannot exceed 1000."`
}

type getDeviceIn struct {
	Tailnet string `json:"tailnet,omitempty" jsonschema:"Tailnet id. Omit when one tailnet is configured, or when the id default is permitted. Required when several tailnets are configured and default is not permitted."`
	Device  string `json:"device" jsonschema:"Device id, hostname, name, or IP address."`
	Scope   string `json:"scope,omitempty" jsonschema:"Device view. mine applies the viewer's identity autoscope and is the default when autoscope is user or groups. all clears that filter and returns every device in the permitted tailnets. A tailnet outside the allowlist is still denied."`
}

type rankedIn struct {
	Tailnet      string   `json:"tailnet,omitempty" jsonschema:"Tailnet id. Omit when one tailnet is configured, or when the id default is permitted. Required when several tailnets are configured and default is not permitted."`
	Start        string   `json:"start,omitempty" jsonschema:"Window start in RFC3339. Defaults to one hour before end."`
	End          string   `json:"end,omitempty" jsonschema:"Window end in RFC3339. Defaults to now. Future times are clamped to now. The window cannot exceed 7 days."`
	TrafficTypes []string `json:"trafficTypes,omitempty" jsonschema:"Traffic types to include: virtual, subnet, exit, physical. Physical transport is excluded unless this list contains physical. DERP relays are labeled DERP relay."`
	Scope        string   `json:"scope,omitempty" jsonschema:"Device view. mine applies the viewer's identity autoscope and is the default when autoscope is user or groups. all clears that filter and returns every device in the permitted tailnets. A tailnet outside the allowlist is still denied."`
	Sort         string   `json:"sort,omitempty" jsonschema:"Order by bytes (default) or flows."`
	Limit        int      `json:"limit,omitempty" jsonschema:"Maximum rows. Defaults to 20 and is clamped to 100."`
	Offset       int      `json:"offset,omitempty" jsonschema:"Rows to skip. Defaults to 0 and cannot exceed 1000."`
}

type flowsBetweenIn struct {
	Tailnet      string   `json:"tailnet,omitempty" jsonschema:"Tailnet id. Omit when one tailnet is configured, or when the id default is permitted. Required when several tailnets are configured and default is not permitted."`
	Start        string   `json:"start,omitempty" jsonschema:"Window start in RFC3339. Defaults to one hour before end."`
	End          string   `json:"end,omitempty" jsonschema:"Window end in RFC3339. Defaults to now. Future times are clamped to now. The window cannot exceed 7 days."`
	TrafficTypes []string `json:"trafficTypes,omitempty" jsonschema:"Traffic types to include: virtual, subnet, exit, physical. Physical transport is excluded unless this list contains physical. DERP relays are labeled DERP relay."`
	A            string   `json:"a" jsonschema:"First endpoint: device id, hostname, name, IP, or CIDR."`
	B            string   `json:"b" jsonschema:"Second endpoint: device id, hostname, name, IP, or CIDR."`
	Scope        string   `json:"scope,omitempty" jsonschema:"Device view. mine applies the viewer's identity autoscope and is the default when autoscope is user or groups. all clears that filter and returns every device in the permitted tailnets. A tailnet outside the allowlist is still denied."`
	Limit        int      `json:"limit,omitempty" jsonschema:"Maximum flow rows. Defaults to 20 and is clamped to 100."`
}

type deviceIn struct {
	Tailnet      string   `json:"tailnet,omitempty" jsonschema:"Tailnet id. Omit when one tailnet is configured, or when the id default is permitted. Required when several tailnets are configured and default is not permitted."`
	Start        string   `json:"start,omitempty" jsonschema:"Window start in RFC3339. Defaults to one hour before end."`
	End          string   `json:"end,omitempty" jsonschema:"Window end in RFC3339. Defaults to now. Future times are clamped to now. The window cannot exceed 7 days."`
	TrafficTypes []string `json:"trafficTypes,omitempty" jsonschema:"Traffic types to include: virtual, subnet, exit, physical. Physical transport is excluded unless this list contains physical. DERP relays are labeled DERP relay."`
	Device       string   `json:"device" jsonschema:"Device id, hostname, name, or IP address."`
	Scope        string   `json:"scope,omitempty" jsonschema:"Device view. mine applies the viewer's identity autoscope and is the default when autoscope is user or groups. all clears that filter and returns every device in the permitted tailnets. A tailnet outside the allowlist is still denied."`
	Limit        int      `json:"limit,omitempty" jsonschema:"Maximum peers. Defaults to 20 and is clamped to 100."`
	Offset       int      `json:"offset,omitempty" jsonschema:"Peers to skip. Defaults to 0 and cannot exceed 1000."`
}

type timelineIn struct {
	Tailnet      string   `json:"tailnet,omitempty" jsonschema:"Tailnet id. Omit when one tailnet is configured, or when the id default is permitted. Required when several tailnets are configured and default is not permitted."`
	Start        string   `json:"start,omitempty" jsonschema:"Window start in RFC3339. Defaults to one hour before end."`
	End          string   `json:"end,omitempty" jsonschema:"Window end in RFC3339. Defaults to now. Future times are clamped to now. The window cannot exceed 7 days."`
	TrafficTypes []string `json:"trafficTypes,omitempty" jsonschema:"Traffic types to split by: virtual, subnet, exit, physical. Physical is omitted unless this list contains physical."`
	Device       string   `json:"device" jsonschema:"Device id, hostname, name, or IP address."`
	Scope        string   `json:"scope,omitempty" jsonschema:"Device view. mine applies the viewer's identity autoscope and is the default when autoscope is user or groups. all clears that filter and returns every device in the permitted tailnets. A tailnet outside the allowlist is still denied."`
}

type newConnectionsIn struct {
	Tailnet      string   `json:"tailnet,omitempty" jsonschema:"Tailnet id. Omit when one tailnet is configured, or when the id default is permitted. Required when several tailnets are configured and default is not permitted."`
	Start        string   `json:"start,omitempty" jsonschema:"Window start in RFC3339. Defaults to one hour before end."`
	End          string   `json:"end,omitempty" jsonschema:"Window end in RFC3339. Defaults to now. Future times are clamped to now. The window cannot exceed 7 days."`
	TrafficTypes []string `json:"trafficTypes,omitempty" jsonschema:"Traffic types to include: virtual, subnet, exit, physical. Physical transport is excluded unless this list contains physical. DERP relays are labeled DERP relay."`
	Lookback     string   `json:"lookback,omitempty" jsonschema:"How far before start a pair must be absent to count as new. Go duration such as 24h. Defaults to 24h and cannot exceed 168h."`
	Scope        string   `json:"scope,omitempty" jsonschema:"Device view. mine applies the viewer's identity autoscope and is the default when autoscope is user or groups. all clears that filter and returns every device in the permitted tailnets. A tailnet outside the allowlist is still denied."`
	Limit        int      `json:"limit,omitempty" jsonschema:"Maximum rows. Defaults to 20 and is clamped to 100."`
	Offset       int      `json:"offset,omitempty" jsonschema:"Rows to skip. Defaults to 0 and cannot exceed 1000."`
}

type statsOverviewIn struct {
	Tailnet      string   `json:"tailnet,omitempty" jsonschema:"Tailnet id. Omit when one tailnet is configured, or when the id default is permitted. Required when several tailnets are configured and default is not permitted."`
	Start        string   `json:"start,omitempty" jsonschema:"Window start in RFC3339. Defaults to one hour before end."`
	End          string   `json:"end,omitempty" jsonschema:"Window end in RFC3339. Defaults to now. Future times are clamped to now. The window cannot exceed 7 days."`
	TrafficTypes []string `json:"trafficTypes,omitempty" jsonschema:"Traffic types to include: virtual, subnet, exit, physical. Physical bytes stay in physicalBytes. Protocol totals include physical only when this list contains physical."`
}

type deviceOut struct {
	ID        string   `json:"id"`
	Name      string   `json:"name,omitempty"`
	Hostname  string   `json:"hostname,omitempty"`
	User      string   `json:"user,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
	OS        string   `json:"os,omitempty"`
	Online    bool     `json:"online"`
	LastSeen  string   `json:"lastSeen,omitempty"`
}

type listTailnetsOut struct {
	Tailnets []handlers.TailnetSummary `json:"tailnets"`
}

type searchDevicesOut struct {
	Tailnet string      `json:"tailnet"`
	Scope   string      `json:"scope"`
	Devices []deviceOut `json:"devices"`
	Limit   int         `json:"limit"`
	Offset  int         `json:"offset"`
	Count   int         `json:"count"`
	HasMore bool        `json:"hasMore"`
}

type getDeviceOut struct {
	Tailnet string    `json:"tailnet"`
	Scope   string    `json:"scope"`
	Device  deviceOut `json:"device"`
}

type talkerOut struct {
	NodeID     string `json:"nodeId"`
	Name       string `json:"name"`
	TxBytes    int64  `json:"txBytes"`
	RxBytes    int64  `json:"rxBytes"`
	TotalBytes int64  `json:"totalBytes"`
	FlowCount  int64  `json:"flowCount"`
}

type pairOut struct {
	SrcNodeID  string `json:"srcNodeId"`
	SrcName    string `json:"srcName"`
	DstNodeID  string `json:"dstNodeId"`
	DstName    string `json:"dstName"`
	TxBytes    int64  `json:"txBytes"`
	RxBytes    int64  `json:"rxBytes"`
	TotalBytes int64  `json:"totalBytes"`
	FlowCount  int64  `json:"flowCount"`
}

type talkersOut struct {
	Tailnet      string      `json:"tailnet"`
	Start        time.Time   `json:"start"`
	End          time.Time   `json:"end"`
	TrafficTypes []string    `json:"trafficTypes,omitempty"`
	Sort         string      `json:"sort"`
	Limit        int         `json:"limit"`
	Offset       int         `json:"offset"`
	Count        int         `json:"count"`
	HasMore      bool        `json:"hasMore"`
	Truncated    bool        `json:"truncated,omitempty"`
	Scope        string      `json:"scope"`
	Talkers      []talkerOut `json:"talkers"`
}

type pairsOut struct {
	Tailnet      string    `json:"tailnet"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	TrafficTypes []string  `json:"trafficTypes,omitempty"`
	Sort         string    `json:"sort"`
	Limit        int       `json:"limit"`
	Offset       int       `json:"offset"`
	Count        int       `json:"count"`
	HasMore      bool      `json:"hasMore"`
	Truncated    bool      `json:"truncated,omitempty"`
	Scope        string    `json:"scope"`
	Pairs        []pairOut `json:"pairs"`
}

type protocolOut struct {
	Protocol int   `json:"protocol"`
	Bytes    int64 `json:"bytes"`
}

type portOut struct {
	Port  int   `json:"port"`
	Proto int   `json:"proto"`
	Bytes int64 `json:"bytes"`
}

type flowOut struct {
	SrcNodeID   string        `json:"srcNodeId"`
	SrcName     string        `json:"srcName"`
	DstNodeID   string        `json:"dstNodeId"`
	DstName     string        `json:"dstName"`
	TrafficType string        `json:"trafficType"`
	TxBytes     int64         `json:"txBytes"`
	RxBytes     int64         `json:"rxBytes"`
	FlowCount   int64         `json:"flowCount"`
	Protocols   []protocolOut `json:"protocols,omitempty"`
	Ports       []portOut     `json:"ports,omitempty"`
}

type flowsOut struct {
	Tailnet      string    `json:"tailnet"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	TrafficTypes []string  `json:"trafficTypes,omitempty"`
	Count        int       `json:"count"`
	HasMore      bool      `json:"hasMore"`
	Scope        string    `json:"scope"`
	Flows        []flowOut `json:"flows"`
}

type peerOut struct {
	NodeID     string `json:"nodeId"`
	Name       string `json:"name"`
	TxBytes    int64  `json:"txBytes"`
	RxBytes    int64  `json:"rxBytes"`
	TotalBytes int64  `json:"totalBytes"`
	FlowCount  int64  `json:"flowCount"`
}

type peersOut struct {
	Tailnet      string    `json:"tailnet"`
	NodeID       string    `json:"nodeId"`
	Name         string    `json:"name"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	TrafficTypes []string  `json:"trafficTypes,omitempty"`
	Limit        int       `json:"limit"`
	Offset       int       `json:"offset"`
	Count        int       `json:"count"`
	HasMore      bool      `json:"hasMore"`
	Truncated    bool      `json:"truncated,omitempty"`
	Scope        string    `json:"scope"`
	Peers        []peerOut `json:"peers"`
}

type timelineBucket struct {
	Time        time.Time        `json:"time"`
	TxBytes     int64            `json:"txBytes"`
	RxBytes     int64            `json:"rxBytes"`
	BytesByType map[string]int64 `json:"bytesByType"`
}

type timelineOut struct {
	Tailnet      string           `json:"tailnet"`
	NodeID       string           `json:"nodeId"`
	Name         string           `json:"name"`
	Start        time.Time        `json:"start"`
	End          time.Time        `json:"end"`
	TrafficTypes []string         `json:"trafficTypes"`
	Truncated    bool             `json:"truncated,omitempty"`
	Scope        string           `json:"scope"`
	Buckets      []timelineBucket `json:"buckets"`
}

type newPairOut struct {
	SrcNodeID string `json:"srcNodeId"`
	SrcName   string `json:"srcName"`
	DstNodeID string `json:"dstNodeId"`
	DstName   string `json:"dstName"`
}

type newConnectionsOut struct {
	Tailnet      string       `json:"tailnet"`
	Start        time.Time    `json:"start"`
	End          time.Time    `json:"end"`
	Lookback     string       `json:"lookback"`
	TrafficTypes []string     `json:"trafficTypes,omitempty"`
	Limit        int          `json:"limit"`
	Offset       int          `json:"offset"`
	Count        int          `json:"count"`
	HasMore      bool         `json:"hasMore"`
	Scope        string       `json:"scope"`
	Pairs        []newPairOut `json:"pairs"`
}

type overviewBucket struct {
	Time            time.Time `json:"time"`
	TCPBytes        int64     `json:"tcpBytes"`
	UDPBytes        int64     `json:"udpBytes"`
	OtherProtoBytes int64     `json:"otherProtoBytes"`
	VirtualBytes    int64     `json:"virtualBytes"`
	ExitBytes       int64     `json:"exitBytes"`
	SubnetBytes     int64     `json:"subnetBytes"`
	PhysicalBytes   int64     `json:"physicalBytes"`
	TotalFlows      int64     `json:"totalFlows"`
	UniquePairs     int64     `json:"uniquePairs"`
}

type statsOverviewOut struct {
	Tailnet      string                `json:"tailnet"`
	Start        time.Time             `json:"start"`
	End          time.Time             `json:"end"`
	Source       string                `json:"source"`
	TrafficTypes []string              `json:"trafficTypes,omitempty"`
	Summary      handlers.StatsSummary `json:"summary"`
	Buckets      []overviewBucket      `json:"buckets"`
}

func (s *Service) addTools(server *mcp.Server, v Viewer) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_tailnets",
		Description: "List tailnets this viewer may query. Each row has an id, display name, and poller status. Credentials are omitted. Access control limits the list to granted tailnets.",
		Annotations: readOnly("List tailnets"),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in listTailnetsIn) (*mcp.CallToolResult, listTailnetsOut, error) {
		out, err := s.listTailnets(v, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_devices",
		Description: "Search devices by name, hostname, IP address, tag, or user login. An empty query lists devices. Results stay inside the viewer's tailnet allowlist. scope defaults to the viewer's autoscope (mine) and scope=all clears that filter, matching the UI.",
		Annotations: readOnly("Search devices"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchDevicesIn) (*mcp.CallToolResult, searchDevicesOut, error) {
		out, err := s.searchDevices(ctx, v, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_device",
		Description: "Get one device by id, hostname, name, or IP, including tags and owner. scope defaults to the viewer's autoscope. scope=all returns any device in a permitted tailnet. A tailnet outside the allowlist is denied.",
		Annotations: readOnly("Get device"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getDeviceIn) (*mcp.CallToolResult, getDeviceOut, error) {
		out, err := s.getDevice(ctx, v, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "top_talkers",
		Description: "Rank devices by traffic over a window using stored rollups. Physical transport is excluded unless trafficTypes includes physical. A DERP relay is labeled DERP relay. scope defaults to the viewer's autoscope; scope=all includes every device in the permitted tailnet. The default window is the last hour and the default limit is 20.",
		Annotations: readOnly("Top talkers"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in rankedIn) (*mcp.CallToolResult, talkersOut, error) {
		out, err := s.topTalkers(ctx, v, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "top_pairs",
		Description: "Rank device pairs by traffic over a window using stored rollups. Physical transport is excluded unless trafficTypes includes physical. DERP relays are labeled DERP relay. scope defaults to the viewer's autoscope and hides pairs touching other devices; scope=all clears that filter. Tailnet allowlists still apply. The default window is the last hour and the default limit is 20.",
		Annotations: readOnly("Top pairs"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in rankedIn) (*mcp.CallToolResult, pairsOut, error) {
		out, err := s.topPairs(ctx, v, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "flows_between",
		Description: "Summarize stored traffic between two endpoints, each a device id, hostname, name, IP, or CIDR. Returns bytes, flow counts, protocols, and destination ports for both directions. Physical transport is excluded unless requested. scope defaults to the viewer's autoscope; scope=all allows any device in a permitted tailnet.",
		Annotations: readOnly("Flows between endpoints"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in flowsBetweenIn) (*mcp.CallToolResult, flowsOut, error) {
		out, err := s.flowsBetween(ctx, v, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "device_peers",
		Description: "List the busiest peers of one device over a window, using stored rollups. Physical transport is excluded unless trafficTypes includes physical. scope defaults to the viewer's autoscope and omits other devices; scope=all clears that filter.",
		Annotations: readOnly("Device peers"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in deviceIn) (*mcp.CallToolResult, peersOut, error) {
		out, err := s.devicePeers(ctx, v, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "device_timeline",
		Description: "Bytes over time for one device, split by traffic type. The default series are virtual, subnet, and exit. Physical transport is included only when requested. scope defaults to the viewer's autoscope; scope=all selects any device in a permitted tailnet. Buckets are capped so the result stays small.",
		Annotations: readOnly("Device timeline"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in timelineIn) (*mcp.CallToolResult, timelineOut, error) {
		out, err := s.deviceTimeline(ctx, v, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "new_connections",
		Description: "List pairs first seen in the window: present in the window and absent from the preceding lookback. Physical pairs are excluded unless requested. DERP relays are labeled DERP relay. scope defaults to the viewer's autoscope; scope=all includes every pair in the permitted tailnet. Results come from stored rollups.",
		Annotations: readOnly("New connections"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in newConnectionsIn) (*mcp.CallToolResult, newConnectionsOut, error) {
		out, err := s.newConnections(ctx, v, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "stats_overview",
		Description: "Network-wide totals for a window: protocol bytes, traffic-type bytes, flows, distinct pairs, and active nodes. This matches the stats overview route. physicalBytes is kept separate. Protocol totals include physical traffic only when trafficTypes requests it. DERP region numbers are not counted as service ports.",
		Annotations: readOnly("Stats overview"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in statsOverviewIn) (*mcp.CallToolResult, statsOverviewOut, error) {
		out, err := s.statsOverview(ctx, v, in)
		return nil, out, err
	})
}
