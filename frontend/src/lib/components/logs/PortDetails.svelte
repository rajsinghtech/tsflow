<script lang="ts">
	import { uiStore, filteredNodes, rawLogs, devices } from '#lib/stores';
	import { formatBytes, extractIP } from '#lib/utils';
	import { portUsageForNode } from '#lib/utils/port-usage';
	import type { NetworkLog } from '#lib/types';

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

	// Resolve address (IP:port or device ID) to IP
	function resolveToNodeIP(address: string): string {
		const ip = extractIP(address);
		return deviceIdToIP.get(ip) || ip;
	}

	const selectedNode = $derived.by(() => {
		const selectedId = $uiStore.selectedNodeId;
		if (!selectedId) return null;
		return $filteredNodes.find((n) => n.id === selectedId) || null;
	});

	const portData = $derived.by(() => {
		if (!selectedNode) return { stats: [], totalCount: 0 };
		const nodeIPs = new Set(selectedNode.ips || []);
		nodeIPs.add(selectedNode.id);
		const allPorts = portUsageForNode($rawLogs, nodeIPs, resolveToNodeIP);
		return {
			stats: allPorts.slice(0, 20),
			totalCount: allPorts.length
		};
	});

	const portStats = $derived(portData.stats);
	const totalPortCount = $derived(portData.totalCount);

	const totalTraffic = $derived.by(() => {
		if (!selectedNode) return { tx: 0, rx: 0, total: 0 };

		const nodeIPs = new Set(selectedNode.ips || []);
		nodeIPs.add(selectedNode.id);
		let tx = 0;
		let rx = 0;

		$rawLogs.forEach((log: NetworkLog) => {
			const allTraffic = [
				...(log.virtualTraffic || []),
				...(log.exitTraffic || []),
				...(log.subnetTraffic || [])
			];

			allTraffic.forEach((t) => {
				const srcIP = resolveToNodeIP(t.src);
				const dstIP = resolveToNodeIP(t.dst);

				// TX-only: selected node's TX = txBytes when node is src
				// Selected node's RX = txBytes when node is dst
				if (nodeIPs.has(srcIP)) {
					tx += t.txBytes || 0;
				} else if (nodeIPs.has(dstIP)) {
					rx += t.txBytes || 0;
				}
			});
		});

		return { tx, rx, total: tx + rx };
	});
</script>

{#if selectedNode && portStats.length > 0}
	<div class="border-t border-border bg-card">
		<div class="border-b border-border p-2 sm:p-3">
			<h2 class="text-sm font-semibold">Port Usage</h2>
			<p class="text-xs text-muted-foreground">
				<span class="hidden sm:inline">{selectedNode.displayName} - </span>{totalPortCount} active ports{totalPortCount > 20 ? ' (top 20)' : ''}
				<span class="ml-2">
					TX: {formatBytes(totalTraffic.tx)} | RX: {formatBytes(totalTraffic.rx)}
				</span>
			</p>
		</div>

		<!-- Desktop/Tablet table -->
		<div class="hidden max-h-48 overflow-auto sm:block">
			<table class="w-full text-xs">
				<thead class="sticky top-0 bg-card">
					<tr class="border-b border-border text-left">
						<th class="px-3 py-1.5 font-medium">Port</th>
						<th class="px-3 py-1.5 font-medium">Service</th>
						<th class="px-3 py-1.5 font-medium">Proto</th>
						<th class="px-3 py-1.5 font-medium text-right" title="Distinct peers on this port">Peers</th>
						<th class="px-3 py-1.5 font-medium text-right">TX</th>
						<th class="px-3 py-1.5 font-medium text-right">RX</th>
						<th class="px-3 py-1.5 font-medium text-right">Total</th>
					</tr>
				</thead>
				<tbody>
					{#each portStats as stat}
						<tr class="border-b border-border/50 hover:bg-secondary/50">
							<td class="px-3 py-1 font-mono text-primary">{stat.port}</td>
							<td class="px-3 py-1">{stat.name}</td>
							<td class="px-3 py-1 uppercase text-muted-foreground">{stat.protocol}</td>
							<td class="px-3 py-1 text-right">{stat.peers}</td>
							<td class="px-3 py-1 text-right text-blue-400">{formatBytes(stat.txBytes)}</td>
							<td class="px-3 py-1 text-right text-emerald-400">{formatBytes(stat.rxBytes)}</td>
							<td class="px-3 py-1 text-right font-medium">{formatBytes(stat.txBytes + stat.rxBytes)}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>

		<!-- Mobile card view -->
		<div class="max-h-48 divide-y divide-border/50 overflow-auto sm:hidden">
			{#each portStats as stat}
				<div class="px-3 py-2">
					<div class="flex items-center justify-between">
						<div class="flex items-center gap-2">
							<span class="font-mono text-xs text-primary">{stat.port}</span>
							<span class="text-xs">{stat.name}</span>
							<span class="text-[10px] uppercase text-muted-foreground">{stat.protocol}</span>
						</div>
						<span class="text-[10px] text-muted-foreground">{stat.peers} {stat.peers === 1 ? 'peer' : 'peers'}</span>
					</div>
					<div class="mt-0.5 flex gap-3 text-[10px]">
						<span class="text-blue-400">TX {formatBytes(stat.txBytes)}</span>
						<span class="text-emerald-400">RX {formatBytes(stat.rxBytes)}</span>
						<span class="font-medium">{formatBytes(stat.txBytes + stat.rxBytes)}</span>
					</div>
				</div>
			{/each}
		</div>
	</div>
{/if}
