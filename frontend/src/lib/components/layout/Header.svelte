<script lang="ts">
	import { RefreshCw, PanelLeft, ScrollText, Sun, Moon, Monitor, Network, Link, Activity, BarChart3, Shield } from 'lucide-svelte';
	import { page } from '$app/stores';
	import { uiStore, loadNetworkData, networkStats, filteredNodes, lastUpdated, isAutoRefreshing, themeStore, statsSummary, topTalkers } from '$lib/stores';
	import { policyGraph } from '$lib/stores/policy-store';
	import { formatBytes } from '$lib/utils';
	import type { ThemeMode } from '$lib/stores';

	// Tick every 10s to keep the relative time fresh
	let tick = $state(0);
	$effect(() => {
		const interval = setInterval(() => tick++, 10_000);
		return () => clearInterval(interval);
	});

	const lastUpdatedLabel = $derived.by(() => {
		void tick; // subscribe to tick for periodic re-computation
		const ts = $lastUpdated;
		if (!ts) return null;
		const now = Date.now();
		const diffSec = Math.floor((now - ts.getTime()) / 1000);
		if (diffSec < 5) return 'just now';
		if (diffSec < 60) return `${diffSec}s ago`;
		const diffMin = Math.floor(diffSec / 60);
		return `${diffMin}m ago`;
	});

	const currentPath = $derived($page.url.pathname);

	let isRefreshing = $state(false);

	async function handleRefresh() {
		isRefreshing = true;
		await loadNetworkData();
		isRefreshing = false;
	}

	// Smart stats: use network store data when available, fall back to stats store
	const hasNetworkData = $derived($networkStats.totalNodes > 0);

	const displayNodes = $derived(hasNetworkData ? $networkStats.totalNodes : $topTalkers.length);
	const displayFlows = $derived(hasNetworkData ? $networkStats.totalConnections : ($statsSummary?.totalFlows ?? 0));
	const displayBytes = $derived.by(() => {
		if (hasNetworkData) return $networkStats.totalBytes;
		if (!$statsSummary) return 0;
		const protoTotal = $statsSummary.tcpBytes + $statsSummary.udpBytes + $statsSummary.otherProtoBytes;
		return protoTotal > 0 ? protoTotal : $statsSummary.virtualBytes + $statsSummary.subnetBytes;
	});

	const avgTrafficPerNode = $derived.by(() => {
		if (displayNodes === 0) return 0;
		return displayBytes / displayNodes;
	});

	const peakNode = $derived.by(() => {
		if ($filteredNodes.length === 0) return null;
		return $filteredNodes.reduce((max, node) =>
			node.totalBytes > max.totalBytes ? node : max
		, $filteredNodes[0]);
	});

	function cycleTheme() {
		themeStore.toggle();
	}

	function getThemeIcon(mode: ThemeMode) {
		switch (mode) {
			case 'light': return Sun;
			case 'dark': return Moon;
			case 'system': return Monitor;
		}
	}

	function getThemeLabel(mode: ThemeMode) {
		switch (mode) {
			case 'light': return 'Light';
			case 'dark': return 'Dark';
			case 'system': return 'System';
		}
	}

	function handleFilterToggle() {
		uiStore.toggleFilters();
	}

	const ThemeIcon = $derived(getThemeIcon($themeStore));
</script>

<header class="flex h-12 items-center justify-between border-b border-border bg-card px-2 sm:h-14 sm:px-4">
	<!-- Left section: Filter toggle + Logo + Nav -->
	<div class="flex items-center gap-2 sm:gap-4">
		<button
			onclick={handleFilterToggle}
			class="rounded-md p-2.5 hover:bg-secondary sm:p-2"
			title="Toggle filter panel"
		>
			<PanelLeft class="h-5 w-5" />
		</button>

		<div class="flex items-center gap-1.5 sm:gap-2">
			<Activity class="h-4 w-4 text-primary sm:h-5 sm:w-5" />
			<h1 class="text-base font-semibold sm:text-lg">TSFlow</h1>
		</div>

		<!-- Navigation -->
		<nav class="flex items-center gap-0.5 sm:gap-1">
			<a
				href="/"
				class="flex items-center gap-1.5 rounded-md px-2.5 py-2 text-sm hover:bg-secondary sm:px-3 sm:py-1.5"
				class:bg-secondary={currentPath === '/'}
			>
				<Network class="h-4 w-4" />
				<span class="hidden sm:inline">Traffic</span>
			</a>
			<a
				href="/policy"
				class="flex items-center gap-1.5 rounded-md px-2.5 py-2 text-sm hover:bg-secondary sm:px-3 sm:py-1.5"
				class:bg-secondary={currentPath === '/policy'}
			>
				<Shield class="h-4 w-4" />
				<span class="hidden sm:inline">Policy</span>
			</a>
			<a
				href="/analytics"
				class="flex items-center gap-1.5 rounded-md px-2.5 py-2 text-sm hover:bg-secondary sm:px-3 sm:py-1.5"
				class:bg-secondary={currentPath === '/analytics'}
			>
				<BarChart3 class="h-4 w-4" />
				<span class="hidden sm:inline">Analytics</span>
			</a>
		</nav>
	</div>

	<!-- Center section: Network Stats (desktop only) -->
	<div class="hidden items-center gap-6 lg:flex">
		<div class="flex items-center gap-2">
			<Network class="h-4 w-4 text-muted-foreground" />
			<div class="text-sm">
				<span class="font-semibold">{displayNodes}</span>
				<span class="text-muted-foreground"> {hasNetworkData ? 'nodes' : 'devices'}</span>
			</div>
		</div>

		<div class="flex items-center gap-2">
			<Link class="h-4 w-4 text-muted-foreground" />
			<div class="text-sm">
				<span class="font-semibold">{hasNetworkData ? displayFlows : displayFlows.toLocaleString()}</span>
				<span class="text-muted-foreground"> flows</span>
			</div>
		</div>

		<div class="h-6 w-px bg-border"></div>

		<div class="text-sm">
			<span class="text-muted-foreground">Traffic:</span>
			<span class="ml-1 font-semibold text-primary">{formatBytes(displayBytes)}</span>
		</div>

		<div class="text-sm">
			<span class="text-muted-foreground">Avg/Node:</span>
			<span class="ml-1 font-semibold">{formatBytes(avgTrafficPerNode)}</span>
		</div>

		{#if peakNode}
			<div class="text-sm" title="{peakNode.displayName} ({peakNode.ip}) - {formatBytes(peakNode.totalBytes)}">
				<span class="text-muted-foreground">Peak:</span>
				<span class="ml-1 font-semibold">{peakNode.displayName}</span>
				<span class="ml-1 text-xs text-muted-foreground">({formatBytes(peakNode.totalBytes)})</span>
			</div>
		{/if}

		{#if lastUpdatedLabel}
			<div class="h-6 w-px bg-border"></div>
			<div class="text-xs text-muted-foreground/70" title={$lastUpdated?.toLocaleString()}>
				Updated {lastUpdatedLabel}
			</div>
		{/if}
	</div>

	<!-- Compact stats for mobile (<md) -->
	<div class="flex items-center gap-2 md:hidden">
		<span class="text-[10px] font-semibold tabular-nums">{displayNodes}<span class="font-normal text-muted-foreground">n</span></span>
		<span class="text-[10px] font-semibold tabular-nums text-primary">{formatBytes(displayBytes)}</span>
	</div>

	<!-- Compact stats for tablet (md only) -->
	<div class="hidden items-center gap-3 md:flex lg:hidden">
		<div class="text-xs">
			<span class="font-semibold">{displayNodes}</span>
			<span class="text-muted-foreground"> {hasNetworkData ? 'nodes' : 'devices'}</span>
		</div>
		<div class="text-xs">
			<span class="font-semibold text-primary">{formatBytes(displayBytes)}</span>
		</div>
		{#if lastUpdatedLabel}
			<div class="text-[10px] text-muted-foreground/60">{lastUpdatedLabel}</div>
		{/if}
	</div>

	<!-- Right section: Actions -->
	<div class="flex items-center gap-1 sm:gap-2">
		<button
			onclick={handleRefresh}
			class="relative flex items-center gap-2 rounded-md p-2.5 hover:bg-secondary sm:px-3 sm:py-1.5"
			disabled={isRefreshing}
			title={$isAutoRefreshing ? 'Auto-refreshing every 60s (R to manual refresh)' : 'Refresh (R)'}
		>
			{#if $isAutoRefreshing}
				<span class="absolute -right-0.5 -top-0.5 flex h-2.5 w-2.5">
					<span class="absolute inline-flex h-full w-full animate-ping rounded-full bg-primary/60"></span>
					<span class="relative inline-flex h-2.5 w-2.5 rounded-full bg-primary"></span>
				</span>
			{/if}
			<RefreshCw class="h-4 w-4 {isRefreshing ? 'animate-spin' : ''}" />
			<span class="hidden text-sm sm:inline">Refresh</span>
		</button>

		<button
			onclick={cycleTheme}
			class="flex items-center gap-2 rounded-md p-2.5 hover:bg-secondary sm:p-2"
			title="Theme: {getThemeLabel($themeStore)} (click to cycle)"
		>
			<ThemeIcon class="h-5 w-5" />
		</button>

		<button
			onclick={() => uiStore.toggleLogViewer()}
			class="rounded-md p-2.5 hover:bg-secondary sm:p-2"
			title="Toggle log viewer"
		>
			<ScrollText class="h-5 w-5" />
		</button>
	</div>
</header>
