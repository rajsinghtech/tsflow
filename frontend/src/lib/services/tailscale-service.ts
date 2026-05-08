import { api } from './api-service';
import type { Device, NetworkLogsResponse, TrafficStatsBucket, TrafficStatsSummary, TopTalker, TopPair, NodeDetailStats } from '$lib/types';

export interface DevicesResponse {
	devices: Device[];
}

export interface ServicesResponse {
	services: Record<string, { name: string; addrs: string[] }>;
	records: Record<string, { addrs: string[]; comment?: string }>;
}

export interface DataRange {
	earliest: string;
	latest: string;
	count: number;
}

export interface StoredFlowLog {
	id: number;
	loggedAt: string;
	nodeId: string;
	periodStart: string;
	periodEnd: string;
	trafficType: string;
	protocol: number;
	srcIp: string;
	srcPort: number;
	dstIp: string;
	dstPort: number;
	txBytes: number;
	rxBytes: number;
	txPkts: number;
	rxPkts: number;
	createdAt: string;
}

export interface StoredFlowLogsResponse {
	logs: StoredFlowLog[];
	metadata: {
		count: number;
		start: string;
		end: string;
		limit: number;
		source: string;
	};
}

export interface AggregatedFlow {
	// Node pair aggregates (device IDs or IPs for external nodes)
	srcNodeId: string;
	dstNodeId: string;
	srcDisplayName?: string;
	dstDisplayName?: string;
	trafficType: string;
	totalTxBytes: number;
	totalRxBytes: number;
	totalTxPkts: number;
	totalRxPkts: number;
	flowCount: number;
	// Legacy fields for backwards compatibility
	nodeId?: string;
	protocol?: number;
	srcIp?: string;
	srcPort?: number;
	dstIp?: string;
	dstPort?: number;
	firstSeen?: string;
	lastSeen?: string;
}

export interface AggregatedFlowsResponse {
	flows: AggregatedFlow[];
	metadata: {
		count: number;
		start: string;
		end: string;
		source: string;
	};
}

export interface PollerStatus {
	running: boolean;
	lastPollTime: string;
	lastPollCount: number;
	totalPolled: number;
	pollErrors: number;
	pollInterval: string;
	database?: {
		tableCounts?: {
			flow_logs_current?: number;
			node_pairs_minutely?: number;
			node_pairs_hourly?: number;
			node_pairs_daily?: number;
			bandwidth_minutely?: number;
			bandwidth_hourly?: number;
			bandwidth_daily?: number;
			bandwidth_by_node_minutely?: number;
			bandwidth_by_node_hourly?: number;
			bandwidth_by_node_daily?: number;
		};
		dbSizeBytes: number;
		dataRange: DataRange;
	};
}

export interface BandwidthBucket {
	time: string;
	txBytes: number;
	rxBytes: number;
}

export interface BandwidthResponse {
	buckets: BandwidthBucket[];
	metadata: {
		count: number;
		start: string;
		end: string;
		bucketSeconds: number;
	};
}

export const tailscaleService = {
	async getDevices(signal?: AbortSignal): Promise<Device[]> {
		const response = await api.get<DevicesResponse>('/devices', { signal });
		return response.devices || [];
	},

	async getNetworkLogs(start: Date, end: Date, signal?: AbortSignal): Promise<NetworkLogsResponse> {
		const startISO = start.toISOString();
		const endISO = end.toISOString();
		return api.get<NetworkLogsResponse>(`/network-logs?start=${startISO}&end=${endISO}`, { signal });
	},

	async getServicesRecords(signal?: AbortSignal): Promise<ServicesResponse> {
		return api.get<ServicesResponse>('/services-records', { signal });
	},

	async getStoredFlowLogs(start: Date, end: Date, limit = 50000, signal?: AbortSignal): Promise<StoredFlowLogsResponse> {
		const startISO = start.toISOString();
		const endISO = end.toISOString();
		return api.get<StoredFlowLogsResponse>(
			`/flow-logs?start=${startISO}&end=${endISO}&limit=${limit}`, { signal }
		);
	},

	async getAggregatedFlows(start: Date, end: Date, trafficTypes?: string[], signal?: AbortSignal): Promise<AggregatedFlowsResponse> {
		const startISO = start.toISOString();
		const endISO = end.toISOString();
		let url = `/flow-logs/aggregated?start=${startISO}&end=${endISO}`;
		if (trafficTypes && trafficTypes.length > 0) {
			url += `&trafficTypes=${trafficTypes.join(',')}`;
		}
		return api.get<AggregatedFlowsResponse>(url, { signal });
	},

	async getDataRange(): Promise<DataRange> {
		return api.get<DataRange>('/flow-logs/range');
	},

	async getPollerStatus(): Promise<PollerStatus> {
		return api.get<PollerStatus>('/poller/status');
	},

	async getBandwidth(
		start: Date,
		end: Date,
		ipsOrNodeId?: string[] | string,
		signal?: AbortSignal,
		trafficTypes?: string[]
	): Promise<BandwidthResponse> {
		const startISO = start.toISOString();
		const endISO = end.toISOString();
		let url = `/bandwidth?start=${startISO}&end=${endISO}`;
		if (ipsOrNodeId) {
			if (Array.isArray(ipsOrNodeId)) {
				// Backwards compatible: pass IPs to resolve to node IDs
				url += `&ips=${ipsOrNodeId.join(',')}`;
			} else {
				// New: pass node ID directly
				url += `&nodeId=${encodeURIComponent(ipsOrNodeId)}`;
			}
		}
		if (trafficTypes && trafficTypes.length > 0) {
			url += `&trafficTypes=${trafficTypes.map(encodeURIComponent).join(',')}`;
		}
		return api.get<BandwidthResponse>(url, { signal });
	},

	async getStatsOverview(start: Date, end: Date, signal?: AbortSignal, trafficTypes?: string[]): Promise<{
		summary: TrafficStatsSummary;
		buckets: TrafficStatsBucket[];
		metadata: { start: string; end: string; bucketCount: number; source: string; trafficTypes?: string[] };
	}> {
		const startISO = start.toISOString();
		const endISO = end.toISOString();
		let url = `/stats/overview?start=${startISO}&end=${endISO}`;
		if (trafficTypes && trafficTypes.length > 0) {
			url += `&trafficTypes=${trafficTypes.map(encodeURIComponent).join(',')}`;
		}
		return api.get(url, { signal });
	},

	async getTopTalkers(start: Date, end: Date, limit = 10, signal?: AbortSignal, trafficTypes?: string[]): Promise<{
		talkers: TopTalker[];
		metadata: { start: string; end: string; limit: number; count: number; trafficTypes?: string[] };
	}> {
		const startISO = start.toISOString();
		const endISO = end.toISOString();
		let url = `/stats/top-talkers?start=${startISO}&end=${endISO}&limit=${limit}`;
		if (trafficTypes && trafficTypes.length > 0) {
			url += `&trafficTypes=${trafficTypes.map(encodeURIComponent).join(',')}`;
		}
		return api.get(url, { signal });
	},

	async getTopPairs(start: Date, end: Date, limit = 10, signal?: AbortSignal, trafficTypes?: string[]): Promise<{
		pairs: TopPair[];
		metadata: { start: string; end: string; limit: number; count: number; trafficTypes?: string[] };
	}> {
		const startISO = start.toISOString();
		const endISO = end.toISOString();
		let url = `/stats/top-pairs?start=${startISO}&end=${endISO}&limit=${limit}`;
		if (trafficTypes && trafficTypes.length > 0) {
			url += `&trafficTypes=${trafficTypes.map(encodeURIComponent).join(',')}`;
		}
		return api.get(url, { signal });
	},

	async getNodeStats(nodeId: string, start: Date, end: Date): Promise<NodeDetailStats> {
		const startISO = start.toISOString();
		const endISO = end.toISOString();
		return api.get(`/stats/node/${encodeURIComponent(nodeId)}?start=${startISO}&end=${endISO}`);
	}
};
