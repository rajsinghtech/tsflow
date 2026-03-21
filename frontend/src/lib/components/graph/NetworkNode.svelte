<script lang="ts">
	import { Handle, Position } from '@xyflow/svelte';
	import { Server, Globe, Network, Radio, Cloud } from 'lucide-svelte';
	import { formatBytes } from '$lib/utils';
	import { highlightedNodeIds, hasSelection } from '$lib/stores/ui-store';
	import type { NetworkNode } from '$lib/types';

	interface Props {
		data: NetworkNode & { label?: string };
	}

	let { data }: Props = $props();

	// Well-known port names
	const WELL_KNOWN_PORTS: Record<number, string> = {
		22: 'SSH',
		53: 'DNS',
		80: 'HTTP',
		443: 'HTTPS',
		3306: 'MySQL',
		5432: 'Postgres',
		6379: 'Redis',
		8080: 'HTTP-Alt',
		8443: 'HTTPS-Alt'
	};

	// Selection state
	const isHighlighted = $derived($highlightedNodeIds.has(data.id));
	const isDimmed = $derived($hasSelection && !isHighlighted);

	// Determine node color based on type
	const nodeColor = $derived.by(() => {
		if (data.isVIPService) return 'var(--color-node-vip)';
		if (data.tags?.includes('derp')) return 'var(--color-node-derp)';
		if (data.isTailscale) return 'var(--color-node-tailscale)';
		if (data.tags?.includes('private')) return 'var(--color-node-private)';
		return 'var(--color-node-public)';
	});

	// Determine icon type
	const iconType = $derived.by(() => {
		if (data.isVIPService) return 'vip';
		if (data.tags?.includes('derp')) return 'derp';
		if (data.isTailscale) return 'tailscale';
		if (data.tags?.includes('private')) return 'private';
		return 'public';
	});

	const ipv4List = $derived((data.ips || []).filter((ip: string) => !ip.includes(':')).slice(0, 2));
	const ipv6List = $derived((data.ips || []).filter((ip: string) => ip.includes(':')).slice(0, 2));
	const displayTags = $derived(
		(data.tags || [])
			.filter((t: string) => t.startsWith('tag:'))
			.map((t: string) => t.replace('tag:', ''))
			.slice(0, 3)
	);

	// Process ports - only show destination/service ports (not ephemeral source ports)
	const displayPorts = $derived.by(() => {
		// Only use incomingPorts (destination ports) - these represent services being accessed
		// outgoingPorts are ephemeral source ports and not meaningful to display
		const allPorts = new Set([...(data.incomingPorts || [])]);
		const portsArray = Array.from(allPorts).sort((a, b) => a - b);

		// Separate well-known from high ports
		const wellKnown = portsArray.filter((p) => p < 1024 || WELL_KNOWN_PORTS[p]);
		const highPorts = portsArray.filter((p) => p >= 1024 && !WELL_KNOWN_PORTS[p]);

		// Show up to 8 ports total
		const shown = [...wellKnown.slice(0, 6), ...highPorts.slice(0, 2)];
		const hidden = portsArray.length - shown.length;

		return { ports: shown, hiddenCount: hidden, total: portsArray.length };
	});

	function getPortLabel(port: number): string {
		return WELL_KNOWN_PORTS[port] || `${port}`;
	}
</script>

<div
	class="min-w-[180px] w-fit rounded-lg border-2 bg-card shadow-lg transition-all duration-200 dark:shadow-lg shadow-md shadow-black/10"
	class:opacity-25={isDimmed}
	class:grayscale={isDimmed}
	class:ring-2={isHighlighted && $hasSelection}
	class:ring-primary={isHighlighted && $hasSelection}
	style="border-color: {nodeColor}"
>
	<Handle type="target" position={Position.Top} class="!opacity-0" />

	<!-- Header -->
	<div class="rounded-t-md px-3 py-2" style="background: color-mix(in srgb, {nodeColor} 15%, var(--color-card))">
		<div class="flex items-start justify-between gap-3">
			<div class="flex items-center gap-2 min-w-0">
				{#if iconType === 'vip'}
					<Cloud class="h-4 w-4 shrink-0" style="color: {nodeColor}" />
				{:else if iconType === 'derp'}
					<Radio class="h-4 w-4 shrink-0" style="color: {nodeColor}" />
				{:else if iconType === 'tailscale'}
					<Server class="h-4 w-4 shrink-0" style="color: {nodeColor}" />
				{:else if iconType === 'private'}
					<Network class="h-4 w-4 shrink-0" style="color: {nodeColor}" />
				{:else}
					<Globe class="h-4 w-4 shrink-0" style="color: {nodeColor}" />
				{/if}
				<span
					class="text-sm font-semibold leading-tight"
					style="color: {nodeColor}"
					title={data.displayName}
				>
					{data.displayName}
				</span>
				{#if data.isVIPService}
					<span class="shrink-0 rounded-full px-1.5 py-0.5 text-[10px] font-bold leading-none text-white" style="background-color: {nodeColor}">
						VIP
					</span>
				{/if}
			</div>
			<div class="shrink-0 text-right whitespace-nowrap">
				<div class="text-xs font-bold text-node-private">{formatBytes(data.totalBytes)}</div>
				<div class="text-xs text-muted-foreground">{data.connections} conn</div>
			</div>
		</div>
		{#if data.user}
			<div class="mt-1 text-xs text-muted-foreground">
				<span class="opacity-70">User:</span>
				{data.user}
			</div>
		{/if}
	</div>

	<!-- Body -->
	<div class="space-y-2 px-3 py-2">
		<!-- IPs -->
		{#if ipv4List.length > 0 || ipv6List.length > 0}
			<div class="space-y-0.5">
				{#each ipv4List as ip}
					<div class="flex items-center gap-1 text-xs">
						<span class="w-8 shrink-0 text-muted-foreground">IPv4:</span>
						<code class="font-mono text-primary" title={ip}>{ip}</code>
					</div>
				{/each}
				{#each ipv6List as ip}
					<div class="flex items-center gap-1 text-xs">
						<span class="w-8 shrink-0 text-muted-foreground">IPv6:</span>
						<code class="font-mono text-primary/80 text-[10px]" title={ip}>{ip}</code>
					</div>
				{/each}
			</div>
		{:else if data.ip}
			<div class="flex items-center gap-1 text-xs">
				<span class="w-8 text-muted-foreground">IP:</span>
				<code class="font-mono text-primary">{data.ip}</code>
			</div>
		{/if}

		<!-- Ports -->
		{#if displayPorts.ports.length > 0}
			<div class="flex flex-wrap gap-1">
				{#each displayPorts.ports as port}
					<span
						class="inline-flex items-center rounded-full border px-1.5 py-0.5 font-mono text-xs
						{port < 1024 || WELL_KNOWN_PORTS[port]
							? 'border-primary/30 bg-primary/10 text-primary'
							: 'border-border bg-muted text-muted-foreground'}"
						title="Port {port}"
					>
						{getPortLabel(port)}
					</span>
				{/each}
				{#if displayPorts.hiddenCount > 0}
					<span class="rounded-full bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">
						+{displayPorts.hiddenCount}
					</span>
				{/if}
			</div>
		{/if}

		<!-- Tags -->
		{#if displayTags.length > 0}
			<div class="flex flex-wrap gap-1">
				{#each displayTags as tag}
					<span class="rounded-full bg-secondary px-2 py-0.5 text-xs text-secondary-foreground">
						{tag}
					</span>
				{/each}
			</div>
		{/if}

	</div>

	<!-- Footer -->
	<div class="flex items-center justify-between border-t border-border px-3 py-1.5">
		<div class="flex items-center gap-1.5">
			{#if data.isVIPService}
				<div class="h-2 w-2 animate-pulse rounded-full bg-node-vip"></div>
				<span class="text-xs text-node-vip">VIP Service</span>
			{:else if data.isTailscale}
				<div class="h-2 w-2 animate-pulse rounded-full bg-node-tailscale"></div>
				<span class="text-xs text-node-tailscale">Tailscale</span>
			{:else if iconType === 'derp'}
				<span class="text-xs text-node-derp">DERP</span>
			{:else if iconType === 'private'}
				<span class="text-xs text-node-private">Private</span>
			{:else}
				<span class="text-xs text-node-public">External</span>
			{/if}
		</div>
		{#if displayPorts.total > 0}
			<span class="text-xs text-muted-foreground">{displayPorts.total} ports</span>
		{/if}
	</div>

	<Handle type="source" position={Position.Bottom} class="!opacity-0" />
</div>
