<script lang="ts">
	import { formatBytes } from '$lib/utils/format';

	interface Props {
		nodes: any[];
		links: any[];
		timeRange: string;
		useCustomTimeRange?: boolean;
		startDate?: string;
		endDate?: string;
	}

	let { nodes, links, timeRange, useCustomTimeRange = false, startDate = '', endDate = '' }: Props = $props();

	const totalTraffic = $derived(nodes.reduce((sum, node) => sum + node.totalBytes, 0));

	const trafficDistribution = $derived.by(() => {
		const virtualBytes = links
			.filter((link) => link.trafficType === 'virtual')
			.reduce((sum, link) => sum + link.totalBytes, 0);
		const subnetBytes = links
			.filter((link) => link.trafficType === 'subnet')
			.reduce((sum, link) => sum + link.totalBytes, 0);
		const physicalBytes = links
			.filter((link) => link.trafficType === 'physical')
			.reduce((sum, link) => sum + link.totalBytes, 0);
		const total = virtualBytes + subnetBytes + physicalBytes;

		return {
			virtual: { bytes: virtualBytes, percent: total > 0 ? (virtualBytes / total) * 100 : 0 },
			subnet: { bytes: subnetBytes, percent: total > 0 ? (subnetBytes / total) * 100 : 0 },
			physical: { bytes: physicalBytes, percent: total > 0 ? (physicalBytes / total) * 100 : 0 },
			total
		};
	});

	const protocols = $derived(new Set(links.map((l) => l.protocol)));

	const avgPerNode = $derived(nodes.length > 0 ? totalTraffic / nodes.length : 0);

	const peakNode = $derived.by(() => {
		if (nodes.length === 0) return null;
		return nodes.reduce((max, node) => (node.totalBytes > max.totalBytes ? node : max));
	});

	const timeDisplay = $derived.by(() => {
		if (useCustomTimeRange && startDate && endDate) {
			return `${new Date(startDate).toLocaleTimeString()} - ${new Date(endDate).toLocaleTimeString()}`;
		}
		return `Last ${timeRange}`;
	});
</script>

<div class="rounded-lg overflow-hidden" style="background: var(--color-bg-surface); border: 1px solid var(--color-border-base); box-shadow: var(--shadow-soft);">
	<!-- Compact Header -->
	<div class="px-4 py-2.5 border-b" style="background: var(--color-bg-interactive); border-color: var(--color-border-base);">
		<div class="flex items-center justify-between">
			<div class="flex items-center gap-2">
				<div class="w-2 h-2 rounded-full animate-pulse" style="background: var(--color-text-success);"></div>
				<h3 class="text-sm font-semibold" style="color: var(--color-text-base);">Network Overview</h3>
			</div>
			<span class="text-xs" style="color: var(--color-text-muted);">
				{timeDisplay}
			</span>
		</div>
	</div>

	<!-- Main Stats Grid -->
	<div class="p-3">
		<div class="grid grid-cols-2 gap-3">
			<!-- Active Nodes -->
			<div class="rounded-lg p-2.5" style="background: var(--color-bg-interactive);">
				<div class="flex items-center justify-between mb-1">
					<span class="text-xs" style="color: var(--color-text-muted);">Active Nodes</span>
					<svg class="w-3.5 h-3.5" style="color: var(--color-text-primary);" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z" />
					</svg>
				</div>
				<div class="text-lg font-bold" style="color: var(--color-text-base);">
					{nodes.length}
				</div>
			</div>

			<!-- Active Connections -->
			<div class="rounded-lg p-2.5" style="background: var(--color-bg-interactive);">
				<div class="flex items-center justify-between mb-1">
					<span class="text-xs" style="color: var(--color-text-muted);">Connections</span>
					<svg class="w-3.5 h-3.5" style="color: var(--color-text-success);" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
					</svg>
				</div>
				<div class="text-lg font-bold" style="color: var(--color-text-base);">
					{links.length}
					<span class="text-xs font-normal ml-1" style="color: var(--color-text-muted);">flows</span>
				</div>
			</div>
		</div>

		<!-- Traffic Volume Bar -->
		<div class="mt-3 rounded-lg p-2.5" style="background: var(--color-bg-interactive);">
			<div class="flex items-center justify-between mb-1.5">
				<span class="text-xs" style="color: var(--color-text-muted);">Total Traffic</span>
				<span class="text-sm font-semibold" style="color: var(--color-text-base);">
					{formatBytes(totalTraffic)}
				</span>
			</div>

			<!-- Traffic Type Distribution Bar -->
			<div class="h-2 rounded-full overflow-hidden" style="background: var(--color-border-interactive);">
				<div class="h-full flex">
					{#if trafficDistribution.total === 0}
						<div class="w-full h-full" style="background: var(--color-border-base);"></div>
					{:else}
						{#if trafficDistribution.virtual.percent > 0}
							<div
								class="h-full transition-all duration-300"
								style="width: {trafficDistribution.virtual.percent}%; background: var(--color-text-primary);"
								title="Virtual: {formatBytes(trafficDistribution.virtual.bytes)} ({trafficDistribution.virtual.percent.toFixed(1)}%)"
							></div>
						{/if}
						{#if trafficDistribution.subnet.percent > 0}
							<div
								class="h-full transition-all duration-300"
								style="width: {trafficDistribution.subnet.percent}%; background: var(--color-text-success);"
								title="Subnet: {formatBytes(trafficDistribution.subnet.bytes)} ({trafficDistribution.subnet.percent.toFixed(1)}%)"
							></div>
						{/if}
						{#if trafficDistribution.physical.percent > 0}
							<div
								class="h-full transition-all duration-300"
								style="width: {trafficDistribution.physical.percent}%; background: var(--color-text-warning);"
								title="Physical: {formatBytes(trafficDistribution.physical.bytes)} ({trafficDistribution.physical.percent.toFixed(1)}%)"
							></div>
						{/if}
					{/if}
				</div>
			</div>

			<!-- Traffic Type Legend -->
			<div class="flex items-center gap-3 mt-2 text-xs">
				<div class="flex items-center gap-1">
					<div class="w-2 h-2 rounded-full" style="background: var(--color-text-primary);"></div>
					<span style="color: var(--color-text-muted);">Virtual</span>
				</div>
				<div class="flex items-center gap-1">
					<div class="w-2 h-2 rounded-full" style="background: var(--color-text-success);"></div>
					<span style="color: var(--color-text-muted);">Subnet</span>
				</div>
				<div class="flex items-center gap-1">
					<div class="w-2 h-2 rounded-full" style="background: var(--color-text-warning);"></div>
					<span style="color: var(--color-text-muted);">Physical</span>
				</div>
			</div>
		</div>

		<!-- Quick Stats Row -->
		<div class="mt-3 grid grid-cols-3 gap-2 text-xs">
			<!-- Protocol Distribution -->
			<div class="text-center">
				<div class="mb-0.5" style="color: var(--color-text-muted);">Protocols</div>
				<div class="font-semibold" style="color: var(--color-text-base);">
					{protocols.size}
				</div>
			</div>

			<!-- Average Bandwidth -->
			<div class="text-center">
				<div class="mb-0.5" style="color: var(--color-text-muted);">Avg/Node</div>
				<div class="font-semibold" style="color: var(--color-text-base);">
					{formatBytes(avgPerNode)}
				</div>
			</div>

			<!-- Peak Node -->
			<div class="text-center">
				<div class="mb-0.5" style="color: var(--color-text-muted);">Peak Node</div>
				<div
					class="font-semibold truncate px-1"
					style="color: var(--color-text-base);"
					title={peakNode ? peakNode.displayName : 'None'}
				>
					{#if peakNode}
						{peakNode.displayName.length > 12
							? peakNode.displayName.substring(0, 10) + '...'
							: peakNode.displayName}
					{:else}
						None
					{/if}
				</div>
			</div>
		</div>
	</div>
</div>
