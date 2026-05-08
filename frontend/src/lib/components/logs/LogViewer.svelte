<script lang="ts">
	import { rawLogs, uiStore, filteredNodes, filteredEdges, processedNetwork, filterStore, debouncedFilterStore, devices, services, primaryMatchedNodes } from '$lib/stores';
	import { formatBytes, formatTime, extractIP, getProtocolName } from '$lib/utils';
	import { ArrowRight, ArrowUpDown } from 'lucide-svelte';
	import type { NetworkLog, TrafficType } from '$lib/types';

	type SortField = 'logged' | 'txBytes' | 'rxBytes' | 'trafficType' | 'protocol';
	let sortField: SortField = $state('logged');
	let sortDir: 'asc' | 'desc' = $state('desc');

	// Derive a display name for a device, skipping "localhost" (common on mobile devices)
	function deviceDisplayName(device: { hostname?: string; name: string }): string {
		const name = device.hostname && device.hostname !== 'localhost'
			? device.hostname
			: device.name.split('.')[0] || device.name;
		return formatUnknownNodeName(name);
	}

	function formatUnknownNodeName(value: string): string {
		if (/^[A-Za-z0-9]{8,}CNTRL$/.test(value)) {
			return `Unknown Tailscale node ${value.slice(0, 4)}`;
		}
		return value;
	}

	// Build IP to device name lookup map
	const ipToDevice = $derived.by(() => {
		const map = new Map<string, string>();
		for (const device of $devices) {
			const displayName = deviceDisplayName(device);
			for (const addr of device.addresses) {
				map.set(addr, displayName);
			}
		}
		return map;
	});

	// Build device ID to device name lookup map (for aggregated flows)
	const deviceIdToName = $derived.by(() => {
		const map = new Map<string, string>();
		for (const device of $devices) {
			const displayName = deviceDisplayName(device);
			map.set(device.id, displayName);
		}
		return map;
	});

	// Build device ID to primary IP mapping (for resolving aggregated flow addresses)
	const deviceIdToIP = $derived.by(() => {
		const map = new Map<string, string>();
		for (const device of $devices) {
			if (device.addresses.length > 0) {
				map.set(device.id, device.addresses[0]);
			}
		}
		return map;
	});

	// Build service IP to service name lookup map
	const ipToService = $derived.by(() => {
		const map = new Map<string, string>();
		for (const [, service] of Object.entries($services)) {
			const displayName = service.name.replace(/^svc:/, '');
			for (const addr of service.addrs || []) {
				map.set(addr, displayName);
			}
		}
		return map;
	});

	// Resolve an address (could be IP:port or device ID) to its IP.
	// Aggregated flows may use device IDs as src/dst instead of IP:port.
	function resolveToNodeIP(address: string): string {
		const ip = extractIP(address);
		return deviceIdToIP.get(ip) || ip;
	}

	// Helper to resolve IP or device ID to device/service name
	function resolveIP(address: string): { ip: string; port?: string; deviceName?: string } {
		const ip = extractIP(address);
		const portMatch = address.match(/:(\d+)$/);
		const port = portMatch ? portMatch[1] : undefined;
		let deviceName = ipToDevice.get(ip);
		if (!deviceName) {
			deviceName = deviceIdToName.get(ip);
		}
		if (!deviceName) {
			deviceName = ipToService.get(ip);
		}
		return { ip, port, deviceName };
	}

	// Flatten traffic entries for display
	interface FlatTrafficEntry {
		logged: string;
		src: string;
		dst: string;
		protocol: string;
		txBytes: number;
		rxBytes: number;
		trafficType: string;
	}

	// Build a set of IPs from primary matched nodes for log filtering
	const primaryMatchedIPs = $derived.by(() => {
		const ips = new Set<string>();
		for (const node of $primaryMatchedNodes) {
			for (const ip of node.ips) {
				ips.add(ip);
			}
		}
		return ips;
	});

	// Filter logs based on selection, search, and traffic type filters
	const filteredLogs = $derived.by(() => {
		const MAX_FILTERED_LOGS = 200;
		const selectedNodeId = $uiStore.selectedNodeId;
		const selectedEdgeId = $uiStore.selectedEdgeId;
		const searchQuery = $debouncedFilterStore.search;
		const logs = $rawLogs;

		if (!selectedNodeId && !selectedEdgeId && !searchQuery) {
			return logs.slice(0, 100);
		}

		let selectedNodeIPs: Set<string> | null = null;
		let selectedEdgeEndpoints: { source: string; target: string } | null = null;

		if (selectedNodeId) {
			const selectedNode = $filteredNodes.find((n) => n.id === selectedNodeId);
			if (selectedNode) {
				selectedNodeIPs = new Set(selectedNode.ips);
			}
		}

		if (selectedEdgeId) {
			const edge = $filteredEdges.find((e) => e.id === selectedEdgeId);
			if (edge) {
				selectedEdgeEndpoints = { source: edge.originalSource, target: edge.originalTarget };
			}
		}

		const result: NetworkLog[] = [];

		for (const log of logs) {
			if (result.length >= MAX_FILTERED_LOGS) break;

			const allTraffic = [
				...(log.virtualTraffic || []),
				...(log.exitTraffic || []),
				...(log.subnetTraffic || []),
				...(log.physicalTraffic || [])
			];

			const matches = allTraffic.some((traffic) => {
				// resolveToNodeIP handles both IP:port (live) and device ID (historical)
				const srcIP = resolveToNodeIP(traffic.src);
				const dstIP = resolveToNodeIP(traffic.dst);

				if (selectedNodeIPs) {
					return selectedNodeIPs.has(srcIP) || selectedNodeIPs.has(dstIP);
				}

				if (selectedEdgeEndpoints) {
					return (
						(srcIP === selectedEdgeEndpoints.source && dstIP === selectedEdgeEndpoints.target) ||
						(srcIP === selectedEdgeEndpoints.target && dstIP === selectedEdgeEndpoints.source)
					);
				}

				if (searchQuery && primaryMatchedIPs.size > 0) {
					return primaryMatchedIPs.has(srcIP) || primaryMatchedIPs.has(dstIP);
				}

				return false;
			});

			if (matches) {
				result.push(log);
			}
		}

		return result;
	});

	// Flatten logs into individual traffic entries
	const flattenedEntries = $derived.by(() => {
		const MAX_ENTRIES = 500;
		const entries: FlatTrafficEntry[] = [];
		const activeTrafficTypes = $debouncedFilterStore.trafficTypes;
		const activeTrafficTypesSet = activeTrafficTypes.length > 0 ? new Set(activeTrafficTypes) : null;
		const selectedNodeId = $uiStore.selectedNodeId;
		const selectedEdgeId = $uiStore.selectedEdgeId;

		let selectedNodeIPsSet: Set<string> | null = null;
		if (selectedNodeId) {
			const selectedNode = $filteredNodes.find((n) => n.id === selectedNodeId);
			if (selectedNode) {
				selectedNodeIPsSet = new Set(selectedNode.ips);
			}
		}

		let selectedEdge: { source: string; target: string } | null = null;
		if (selectedEdgeId) {
			const edge = $filteredEdges.find((e) => e.id === selectedEdgeId);
			if (edge) {
				selectedEdge = { source: edge.originalSource, target: edge.originalTarget };
			}
		}

		const processTraffic = (traffic: any[] | undefined, type: TrafficType, logged: string): boolean => {
			if (!traffic) return true;
			if (activeTrafficTypesSet && !activeTrafficTypesSet.has(type)) return true;

			for (const t of traffic) {
				if (entries.length >= MAX_ENTRIES) return false;

				const srcIP = resolveToNodeIP(t.src);
				const dstIP = resolveToNodeIP(t.dst);

				if (selectedNodeIPsSet) {
					if (!selectedNodeIPsSet.has(srcIP) && !selectedNodeIPsSet.has(dstIP)) continue;
				}

				if (selectedEdge) {
					const matchesEdge =
						(srcIP === selectedEdge.source && dstIP === selectedEdge.target) ||
						(srcIP === selectedEdge.target && dstIP === selectedEdge.source);
					if (!matchesEdge) continue;
				}

				entries.push({
					logged,
					src: t.src,
					dst: t.dst,
					protocol: getProtocolName(t.proto || 0),
					txBytes: t.txBytes || 0,
					rxBytes: t.rxBytes || 0,
					trafficType: type
				});
			}
			return true;
		};

		for (const log of filteredLogs) {
			if (entries.length >= MAX_ENTRIES) break;
			if (!processTraffic(log.virtualTraffic, 'virtual', log.logged)) break;
			if (!processTraffic(log.exitTraffic, 'exit', log.logged)) break;
			if (!processTraffic(log.subnetTraffic, 'subnet', log.logged)) break;
			if (!processTraffic(log.physicalTraffic, 'physical', log.logged)) break;
		}

		return entries;
	});

	const isTruncated = $derived(flattenedEntries.length >= 500);
	const hasRawLogs = $derived($rawLogs.length > 0);

	// Sort entries
	const sortedEntries = $derived.by(() => {
		if (sortField === 'logged' && sortDir === 'desc') return flattenedEntries; // default order
		const mul = sortDir === 'desc' ? -1 : 1;
		return [...flattenedEntries].sort((a, b) => {
			switch (sortField) {
				case 'logged': return mul * (new Date(a.logged).getTime() - new Date(b.logged).getTime());
				case 'txBytes': return mul * (a.txBytes - b.txBytes);
				case 'rxBytes': return mul * (a.rxBytes - b.rxBytes);
				case 'trafficType': return mul * a.trafficType.localeCompare(b.trafficType);
				case 'protocol': return mul * a.protocol.localeCompare(b.protocol);
				default: return 0;
			}
		});
	});

	// Summary stats
	const entrySummary = $derived.by(() => {
		let totalTx = 0, totalRx = 0;
		for (const e of flattenedEntries) {
			totalTx += e.txBytes;
			totalRx += e.rxBytes;
		}
		return { count: flattenedEntries.length, totalTx, totalRx };
	});

	function toggleSort(field: SortField) {
		if (sortField === field) {
			sortDir = sortDir === 'desc' ? 'asc' : 'desc';
		} else {
			sortField = field;
			sortDir = 'desc';
		}
	}

	function sortIndicator(field: SortField): string {
		if (sortField !== field) return '';
		return sortDir === 'desc' ? ' \u25BE' : ' \u25B4';
	}

	function getTrafficTypeColor(type: string): string {
		switch (type) {
			case 'virtual': return 'text-traffic-virtual';
			case 'exit': return 'text-purple-400';
			case 'subnet': return 'text-traffic-subnet';
			case 'physical': return 'text-traffic-physical';
			default: return 'text-muted-foreground';
		}
	}

	function getTrafficTypeBgColor(type: string): string {
		switch (type) {
			case 'virtual': return 'bg-traffic-virtual/20';
			case 'exit': return 'bg-purple-500/20';
			case 'subnet': return 'bg-traffic-subnet/20';
			case 'physical': return 'bg-traffic-physical/20';
			default: return 'bg-muted';
		}
	}

	function capitalizeFirst(str: string): string {
		return str.charAt(0).toUpperCase() + str.slice(1);
	}

	function handleLogClick(entry: FlatTrafficEntry) {
		const srcIP = resolveToNodeIP(entry.src);
		const dstIP = resolveToNodeIP(entry.dst);

		const findEdgeByIPs = (edges: typeof $filteredEdges) => {
			return edges.find((edge) => {
				return (
					(edge.originalSource === srcIP && edge.originalTarget === dstIP) ||
					(edge.originalSource === dstIP && edge.originalTarget === srcIP)
				);
			});
		};

		let matchingEdge = findEdgeByIPs($filteredEdges);
		if (!matchingEdge) {
			matchingEdge = findEdgeByIPs($processedNetwork.links);
		}

		if (matchingEdge) {
			uiStore.selectEdge(matchingEdge.id);
		}
	}
</script>

<div class="flex h-full flex-col">
	<div class="flex items-center justify-between border-b border-border p-2 sm:p-3">
		<div>
			<h2 class="text-sm font-semibold">Network Logs</h2>
			<p class="text-xs text-muted-foreground">
				{#if $uiStore.selectedNodeId}
					Showing logs for selected node
				{:else if $uiStore.selectedEdgeId}
					Showing logs for selected connection
				{:else}
					Showing recent logs (select a node or edge to filter)
				{/if}
				{#if $filterStore.trafficTypes.length > 0}
					<span class="text-primary"> · Filtered: {$filterStore.trafficTypes.join(', ')}</span>
				{/if}
			</p>
		</div>
		{#if sortField !== 'logged' || sortDir !== 'desc'}
			<button
				class="hidden items-center gap-1 rounded px-2 py-1 text-xs text-muted-foreground hover:bg-secondary sm:flex"
				onclick={() => { sortField = 'logged'; sortDir = 'desc'; }}
			>
				<ArrowUpDown class="h-3 w-3" />
				Reset sort
			</button>
		{/if}
	</div>

	<div class="flex-1 overflow-auto">
		<!-- Desktop/Tablet table view -->
		<table class="hidden w-full text-xs sm:table">
			<thead class="sticky top-0 bg-card">
				<tr class="border-b border-border text-left">
					<th class="cursor-pointer select-none px-2 py-1.5 font-medium transition-colors hover:text-foreground" onclick={() => toggleSort('logged')}>Time{sortIndicator('logged')}</th>
					<th class="cursor-pointer select-none px-2 py-1.5 font-medium transition-colors hover:text-foreground" onclick={() => toggleSort('trafficType')}>Type{sortIndicator('trafficType')}</th>
					<th class="px-2 py-1.5 font-medium">Flow</th>
					<th class="cursor-pointer select-none px-2 py-1.5 font-medium transition-colors hover:text-foreground" onclick={() => toggleSort('protocol')}>Proto{sortIndicator('protocol')}</th>
					<th class="cursor-pointer select-none px-2 py-1.5 font-medium text-right transition-colors hover:text-foreground" onclick={() => toggleSort('txBytes')}>TX{sortIndicator('txBytes')}</th>
					<th class="cursor-pointer select-none px-2 py-1.5 font-medium text-right transition-colors hover:text-foreground" onclick={() => toggleSort('rxBytes')}>RX{sortIndicator('rxBytes')}</th>
				</tr>
			</thead>
			<tbody>
				{#each sortedEntries as entry}
					{@const srcResolved = resolveIP(entry.src)}
					{@const dstResolved = resolveIP(entry.dst)}
					<tr
						class="cursor-pointer border-b border-border/50 hover:bg-secondary/50"
						onclick={() => handleLogClick(entry)}
						role="button"
						tabindex="0"
						onkeydown={(e) => e.key === 'Enter' && handleLogClick(entry)}
					>
						<td class="whitespace-nowrap px-2 py-1 text-muted-foreground">
							{formatTime(entry.logged)}
						</td>
						<td class="px-2 py-1">
							<span class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 {getTrafficTypeBgColor(entry.trafficType)} {getTrafficTypeColor(entry.trafficType)}">
								<span class="h-1.5 w-1.5 rounded-full bg-current"></span>
								{capitalizeFirst(entry.trafficType)}
							</span>
						</td>
						<td class="px-2 py-1">
							<div class="flex items-center gap-1">
								<span class="truncate" title={srcResolved.deviceName ? `${srcResolved.deviceName} (${srcResolved.ip}${srcResolved.port ? ':' + srcResolved.port : ''})` : entry.src}>
									{#if srcResolved.deviceName}
										<span class="text-primary">{srcResolved.deviceName}</span>{#if srcResolved.port}<span class="text-muted-foreground">:{srcResolved.port}</span>{/if}
									{:else}
										{entry.src}
									{/if}
								</span>
								<ArrowRight class="h-3 w-3 shrink-0 text-muted-foreground" />
								<span class="truncate" title={dstResolved.deviceName ? `${dstResolved.deviceName} (${dstResolved.ip}${dstResolved.port ? ':' + dstResolved.port : ''})` : entry.dst}>
									{#if dstResolved.deviceName}
										<span class="text-primary">{dstResolved.deviceName}</span>{#if dstResolved.port}<span class="text-muted-foreground">:{dstResolved.port}</span>{/if}
									{:else}
										{entry.dst}
									{/if}
								</span>
							</div>
						</td>
						<td class="px-2 py-1">
							<span class="font-mono">{entry.protocol}</span>
						</td>
						<td class="whitespace-nowrap px-2 py-1 text-right">{formatBytes(entry.txBytes)}</td>
						<td class="whitespace-nowrap px-2 py-1 text-right">{formatBytes(entry.rxBytes)}</td>
					</tr>
				{/each}
			</tbody>
		</table>

		<!-- Mobile card view -->
		<div class="divide-y divide-border/50 sm:hidden">
			{#each sortedEntries as entry}
				{@const srcResolved = resolveIP(entry.src)}
				{@const dstResolved = resolveIP(entry.dst)}
				<button
					class="w-full px-3 py-2 text-left active:bg-secondary/50"
					onclick={() => handleLogClick(entry)}
				>
					<div class="flex items-center justify-between">
						<span class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] {getTrafficTypeBgColor(entry.trafficType)} {getTrafficTypeColor(entry.trafficType)}">
							<span class="h-1.5 w-1.5 rounded-full bg-current"></span>
							{capitalizeFirst(entry.trafficType)}
						</span>
						<span class="font-mono text-[10px] text-muted-foreground">{entry.protocol}</span>
					</div>
					<div class="mt-1 flex items-center gap-1 text-xs">
						<span class="truncate">
							{#if srcResolved.deviceName}
								<span class="text-primary">{srcResolved.deviceName}</span>
							{:else}
								<span class="font-mono text-[10px]">{srcResolved.ip}</span>
							{/if}
						</span>
						<ArrowRight class="h-3 w-3 shrink-0 text-muted-foreground" />
						<span class="truncate">
							{#if dstResolved.deviceName}
								<span class="text-primary">{dstResolved.deviceName}</span>
							{:else}
								<span class="font-mono text-[10px]">{dstResolved.ip}</span>
							{/if}
						</span>
					</div>
					<div class="mt-0.5 flex items-center justify-between text-[10px] text-muted-foreground">
						<span>{formatTime(entry.logged)}</span>
						<span>TX {formatBytes(entry.txBytes)} / RX {formatBytes(entry.rxBytes)}</span>
					</div>
				</button>
			{/each}
		</div>

		{#if flattenedEntries.length === 0}
			<div class="flex h-32 flex-col items-center justify-center gap-1 text-sm text-muted-foreground">
				{#if !hasRawLogs}
					<p>No traffic data available</p>
					<p class="text-xs text-muted-foreground/60">Waiting for network flow logs from the poller</p>
				{:else if $uiStore.selectedNodeId || $uiStore.selectedEdgeId}
					<p>No matching flows for this selection</p>
					<p class="text-xs text-muted-foreground/60">Try selecting a different node or edge</p>
				{:else if $debouncedFilterStore.search}
					<p>No flows match your search</p>
					<p class="text-xs text-muted-foreground/60">Try broadening your search query</p>
				{:else}
					<p>No flows match current filters</p>
					<p class="text-xs text-muted-foreground/60">Check traffic type filters in the sidebar</p>
				{/if}
			</div>
		{/if}

		{#if entrySummary.count > 0}
			<div class="border-t border-border bg-secondary/30 px-3 py-1.5 text-xs text-muted-foreground">
				<div class="flex items-center justify-between">
					<span>{entrySummary.count} entries{isTruncated ? ' (capped)' : ''}</span>
					<span class="tabular-nums">TX {formatBytes(entrySummary.totalTx)} / RX {formatBytes(entrySummary.totalRx)}</span>
				</div>
			</div>
		{/if}
	</div>
</div>
