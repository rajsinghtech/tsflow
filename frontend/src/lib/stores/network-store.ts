import { writable, derived, get } from 'svelte/store';
import type { Device, NetworkLog, NetworkNode, NetworkLink } from '$lib/types';
import { tailscaleService, type AggregatedFlow } from '$lib/services';
import { processNetworkLogs } from '$lib/utils/network-processor';
import { filterStore, debouncedFilterStore, timeRangeStore, TIME_RANGES } from './filter-store';
import { uiStore } from './ui-store';
import { dataSourceStore } from './data-source-store';

// Last updated timestamp
export const lastUpdated = writable<Date | null>(null);

// Raw data stores
export const devices = writable<Device[]>([]);
export const networkLogs = writable<NetworkLog[]>([]); // Used for graph (aggregated in historical mode)
export const rawLogs = writable<NetworkLog[]>([]); // Used for LogViewer (always full detail with ports)
export const services = writable<Record<string, { name: string; addrs: string[]; tags?: string[] }>>({});
export const records = writable<Record<string, { addrs: string[]; comment?: string }>>({});

// Processed network data
export const processedNetwork = derived(
	[networkLogs, devices, services, records],
	([$logs, $devices, $services, $records]) => {
		return processNetworkLogs($logs, $devices, $services, $records);
	}
);

// First, filter edges by traffic type to determine which nodes should be visible
// Uses debouncedFilterStore to avoid re-running expensive graph filtering on every keystroke
const trafficFilteredEdges = derived([processedNetwork, debouncedFilterStore], ([$network, $filters]) => {
	return $network.links.filter((link) => {
		// Traffic type filter — only show selected types. Empty = show all (defensive fallback).
		if ($filters.trafficTypes.length === 0) return true;
		return $filters.trafficTypes.includes(link.trafficType);
	});
});

// Get node IDs that have at least one connection after traffic type filtering
const nodesWithTrafficConnections = derived(trafficFilteredEdges, ($edges) => {
	const nodeIds = new Set<string>();
	$edges.forEach((edge) => {
		nodeIds.add(edge.source);
		nodeIds.add(edge.target);
	});
	return nodeIds;
});

// Helper to check if a node matches the search query
function nodeMatchesSearch(node: NetworkNode, query: string): boolean {
	if (!query) return true;

	const q = query.toLowerCase().trim();

	if (q.startsWith('tag:')) {
		const tagSearch = q.substring(4);
		const nodeTagsLower = node.tags.map((t) => t.toLowerCase().replace('tag:', ''));
		return nodeTagsLower.some((tag) => tag.includes(tagSearch));
	} else if (q.startsWith('ip:')) {
		const ipSearch = q.substring(3);
		return node.ips.some((ip) => ip.toLowerCase().includes(ipSearch));
	} else if (q.includes('@')) {
		return node.user?.toLowerCase().includes(q.replace('user@', '')) || false;
	} else {
		const matchesIP = node.ips.some((ip) => ip.toLowerCase().includes(q));
		const matchesName = node.displayName.toLowerCase().includes(q);
		const matchesUser = node.user?.toLowerCase().includes(q) || false;
		const matchesTags = node.tags.some((tag) =>
			tag.toLowerCase().replace('tag:', '').includes(q)
		);
		return matchesIP || matchesName || matchesUser || matchesTags;
	}
}

// Primary matched nodes (nodes directly matching the search query)
export const primaryMatchedNodes = derived(
	[processedNetwork, debouncedFilterStore, nodesWithTrafficConnections],
	([$network, $filters, $connectedNodeIds]) => {
		return $network.nodes.filter((node) => {
			if (!$connectedNodeIds.has(node.id)) return false;
			return nodeMatchesSearch(node, $filters.search);
		});
	}
);

// Get IDs of nodes connected to primary matches
const connectedToMatchedNodes = derived(
	[primaryMatchedNodes, trafficFilteredEdges],
	([$primaryNodes, $edges]) => {
		const primaryIds = new Set($primaryNodes.map((n) => n.id));
		const connectedIds = new Set<string>();

		// If no search filter, no need to expand - primaryIds contains all valid nodes
		if (primaryIds.size === 0) return connectedIds;

		// Find all nodes connected to primary matches
		$edges.forEach((edge) => {
			if (primaryIds.has(edge.source)) {
				connectedIds.add(edge.target);
			}
			if (primaryIds.has(edge.target)) {
				connectedIds.add(edge.source);
			}
		});

		return connectedIds;
	}
);

// Filtered nodes: primary matches + their connected nodes
export const filteredNodes = derived(
	[processedNetwork, primaryMatchedNodes, connectedToMatchedNodes, nodesWithTrafficConnections, debouncedFilterStore],
	([$network, $primaryNodes, $connectedIds, $connectedNodeIds, $filters]) => {
		const primaryIds = new Set($primaryNodes.map((n) => n.id));

		// If no search, return all nodes with connections
		if (!$filters.search) {
			return $network.nodes.filter((node) => $connectedNodeIds.has(node.id));
		}

		// Return primary matches + connected nodes
		return $network.nodes.filter((node) => {
			if (!$connectedNodeIds.has(node.id)) return false;
			return primaryIds.has(node.id) || $connectedIds.has(node.id);
		});
	}
);

// Filtered edges - show edges where at least one endpoint is a primary match
export const filteredEdges = derived(
	[trafficFilteredEdges, filteredNodes, primaryMatchedNodes, debouncedFilterStore],
	([$trafficEdges, $nodes, $primaryNodes, $filters]) => {
		const nodeIds = new Set($nodes.map((n) => n.id));
		const primaryIds = new Set($primaryNodes.map((n) => n.id));

		return $trafficEdges.filter((link) => {
			// Both nodes must be visible
			if (!nodeIds.has(link.source) || !nodeIds.has(link.target)) return false;

			// If searching, at least one endpoint must be a primary match
			if ($filters.search) {
				return primaryIds.has(link.source) || primaryIds.has(link.target);
			}

			return true;
		});
	}
);

// Network stats
// Use edge-based totalBytes to avoid double-counting (each byte appears on both
// the sending and receiving node, but only once on the edge connecting them)
export const networkStats = derived([filteredNodes, filteredEdges], ([$nodes, $edges]) => {
	const totalBytes = $edges.reduce((sum, e) => sum + e.totalBytes, 0);
	const totalConnections = $edges.length;
	const tailscaleNodes = $nodes.filter((n) => n.isTailscale).length;
	const externalNodes = $nodes.length - tailscaleNodes;

	return {
		totalNodes: $nodes.length,
		totalConnections,
		totalBytes,
		tailscaleNodes,
		externalNodes
	};
});

// Combined store for export
export const networkStore = {
	subscribe: derived(
		[devices, networkLogs, processedNetwork, filteredNodes, filteredEdges, networkStats],
		([$devices, $logs, $processed, $filteredNodes, $filteredEdges, $stats]) => ({
			devices: $devices,
			logs: $logs,
			nodes: $processed.nodes,
			links: $processed.links,
			filteredNodes: $filteredNodes,
			filteredEdges: $filteredEdges,
			stats: $stats
		})
	).subscribe
};

// Retry state
const MAX_RETRIES = 3;
export const retryCount = writable(0);
export const retryingIn = writable<number | null>(null); // seconds until next retry
let retryTimeout: ReturnType<typeof setTimeout> | null = null;
let retryTickInterval: ReturnType<typeof setInterval> | null = null;

function clearRetryState() {
	if (retryTimeout) {
		clearTimeout(retryTimeout);
		retryTimeout = null;
	}
	if (retryTickInterval) {
		clearInterval(retryTickInterval);
		retryTickInterval = null;
	}
	retryCount.set(0);
	retryingIn.set(null);
}

function scheduleRetry(attempt: number) {
	// Clear any existing tick interval from a previous retry
	if (retryTickInterval) {
		clearInterval(retryTickInterval);
	}

	const delaySec = Math.pow(2, attempt - 1); // 1s, 2s, 4s
	retryingIn.set(delaySec);

	// Countdown ticker
	let remaining = delaySec;
	retryTickInterval = setInterval(() => {
		remaining--;
		if (remaining > 0) {
			retryingIn.set(remaining);
		} else {
			if (retryTickInterval) {
				clearInterval(retryTickInterval);
				retryTickInterval = null;
			}
		}
	}, 1000);

	retryTimeout = setTimeout(() => {
		retryingIn.set(null);
		loadNetworkData(attempt);
	}, delaySec * 1000);
}

// AbortController for in-flight requests
let activeController: AbortController | null = null;

// Load network data
export async function loadNetworkData(currentAttempt = 0) {
	// Cancel any in-flight request
	if (activeController) {
		activeController.abort();
	}
	activeController = new AbortController();
	const signal = activeController.signal;

	uiStore.setLoading(true);
	uiStore.setError(null);

	try {
		// Check data source mode
		const dataSource = get(dataSourceStore);
		let start: Date, end: Date;

		if (dataSource.mode === 'historical') {
			if (!dataSource.selectedStart || !dataSource.selectedEnd) {
				// Range not set yet (e.g. during mode switch) - skip load
				uiStore.setLoading(false);
				return;
			}
			start = dataSource.selectedStart;
			end = dataSource.selectedEnd;
			// Guard against zero/invalid dates from empty DB
			if (start.getFullYear() < 1970 || end.getFullYear() < 1970 || start >= end) {
				uiStore.setLoading(false);
				uiStore.setError('No stored data available yet. Switch to Live mode.');
				return;
			}
		} else {
			// Live mode - use time range store
			const timeRange = get(timeRangeStore);

			if (timeRange.selected === 'custom' && timeRange.customStart && timeRange.customEnd) {
				start = timeRange.customStart;
				end = timeRange.customEnd;
			} else {
				const preset = TIME_RANGES.find((p) => p.value === timeRange.selected);

				end = new Date();
				start = new Date(end.getTime() - (preset?.minutes || 5) * 60 * 1000);
			}
		}

		// Fetch devices and services (always from live API)
		const [devicesData, servicesData] = await Promise.all([
			tailscaleService.getDevices(signal),
			tailscaleService.getServicesRecords(signal)
		]);

		// Fetch logs based on mode
		let graphLogs;
		let viewerLogs;
		if (dataSource.mode === 'historical') {
			// Fetch all traffic types — client-side filtering (trafficFilteredEdges)
			// handles showing/hiding by type. Server-side filtering would cause
			// missing data when the user enables a type that wasn't initially fetched.
			const aggregatedData = await tailscaleService.getAggregatedFlows(start, end, undefined, signal);
			graphLogs = convertAggregatedFlowsToNetworkLogs(aggregatedData.flows || [], start, end);
			// Use aggregated flows for LogViewer too (raw flow_logs_current is empty for old data)
			viewerLogs = graphLogs;
		} else {
			// Fetch live from Tailscale API - same data for both
			const logsData = await tailscaleService.getNetworkLogs(start, end, signal);
			graphLogs = logsData.logs || [];
			viewerLogs = graphLogs;
		}

		if (signal.aborted) return;

		devices.set(devicesData);
		networkLogs.set(graphLogs); // For graph visualization
		rawLogs.set(viewerLogs); // For LogViewer with full detail
		services.set(servicesData.services || {});
		records.set(servicesData.records || {});
		lastUpdated.set(new Date());
		clearRetryState();
	} catch (err) {
		if (signal.aborted) return; // Silently ignore cancelled requests
		console.error('Failed to load network data:', err);
		uiStore.setError(err instanceof Error ? err.message : 'Failed to load network data');

		const nextAttempt = currentAttempt + 1;
		retryCount.set(nextAttempt);
		if (nextAttempt < MAX_RETRIES) {
			scheduleRetry(nextAttempt);
		}
	} finally {
		if (!signal.aborted) {
			uiStore.setLoading(false);
		}
	}
}

// Manual retry with reset backoff
export function retryLoadNetworkData() {
	clearRetryState();
	loadNetworkData(0);
}

// Convert pre-aggregated node-pair flows to NetworkLog format for the graph.
// Emits two entries per flow (forward + reverse with TX-only) so the network
// processor's TX-only dedup logic works consistently for both live and historical data.
function convertAggregatedFlowsToNetworkLogs(flows: AggregatedFlow[], rangeStart: Date, rangeEnd: Date): NetworkLog[] {
	// Build two NetworkLogs per flow: one for the forward direction (src→dst)
	// and one for the reverse (dst→src). Each only carries txBytes.
	const logsByNode = new Map<string, NetworkLog>();
	const startISO = rangeStart.toISOString();
	const endISO = rangeEnd.toISOString();

	function getOrCreateLog(nodeId: string): NetworkLog {
		let log = logsByNode.get(nodeId);
		if (!log) {
			log = {
				logged: endISO,
				nodeId,
				start: startISO,
				end: endISO,
				virtualTraffic: [],
				exitTraffic: [],
				subnetTraffic: [],
				physicalTraffic: []
			};
			logsByNode.set(nodeId, log);
		}
		return log;
	}

	function pushTraffic(log: NetworkLog, trafficType: string, entry: any) {
		switch (trafficType) {
			case 'virtual':
				log.virtualTraffic.push(entry);
				break;
			case 'exit':
				log.exitTraffic!.push(entry);
				break;
			case 'subnet':
				log.subnetTraffic.push(entry);
				break;
			case 'physical':
				log.physicalTraffic.push(entry);
				break;
			default:
				log.virtualTraffic.push(entry);
				break;
		}
	}

	for (const flow of flows) {
		const proto = flow.protocol || 0;

		// Forward direction: src sent txBytes to dst
		if (flow.totalTxBytes > 0) {
			const fwdLog = getOrCreateLog(flow.srcNodeId);
			pushTraffic(fwdLog, flow.trafficType, {
				proto,
				src: flow.srcNodeId,
				dst: flow.dstNodeId,
				txBytes: flow.totalTxBytes,
				rxBytes: 0,
				txPkts: flow.totalTxPkts || 0,
				rxPkts: 0
			});
		}

		// Reverse direction: dst sent rxBytes back to src
		if (flow.totalRxBytes > 0) {
			const revLog = getOrCreateLog(flow.dstNodeId);
			pushTraffic(revLog, flow.trafficType, {
				proto,
				src: flow.dstNodeId,
				dst: flow.srcNodeId,
				txBytes: flow.totalRxBytes,
				rxBytes: 0,
				txPkts: flow.totalRxPkts || 0,
				rxPkts: 0
			});
		}
	}

	return Array.from(logsByNode.values());
}

// Centralized auto-refresh interval (5 minutes)
export const AUTO_REFRESH_INTERVAL = 300_000;

// Auto-refresh state
export const isAutoRefreshing = writable(false);

// Refresh data periodically
let refreshInterval: ReturnType<typeof setInterval> | null = null;

// Clean up on page unload to prevent memory leaks
if (typeof window !== 'undefined') {
	window.addEventListener('beforeunload', () => {
		stopAutoRefresh();
		clearRetryState();
	});
}

export function startAutoRefresh(intervalMs = AUTO_REFRESH_INTERVAL) {
	stopAutoRefresh();
	// Don't auto-refresh in historical mode - data is static
	const ds = get(dataSourceStore);
	if (ds.mode === 'historical') return;
	refreshInterval = setInterval(() => loadNetworkData(0), intervalMs);
	isAutoRefreshing.set(true);
}

export function stopAutoRefresh() {
	if (refreshInterval) {
		clearInterval(refreshInterval);
		refreshInterval = null;
	}
	isAutoRefreshing.set(false);
}

export function toggleAutoRefresh() {
	if (get(isAutoRefreshing)) {
		stopAutoRefresh();
	} else {
		startAutoRefresh();
	}
}
