<script lang="ts">
	import { RefreshCw, PanelLeft, ScrollText, Sun, Moon, Monitor, Network, Link, Activity, BarChart3, Shield, Pause, Play, ExternalLink, Info } from 'lucide-svelte';
	import { fly } from 'svelte/transition';
	import { page } from '$app/stores';
	import { uiStore, loadNetworkData, networkStats, filteredNodes, lastUpdated, isAutoRefreshing, toggleAutoRefresh, themeStore, statsSummary, topTalkers } from '$lib/stores';
	import { policyGraph } from '$lib/stores/policy-store';
	import { formatBytes, formatDuration } from '$lib/utils';
	import type { ThemeMode } from '$lib/stores';

	// Tick every 10s to keep the relative time fresh
	let tick = $state(0);
	$effect(() => {
		const interval = setInterval(() => tick++, 10_000);
		return () => clearInterval(interval);
	});

	// About flyout state
	let showAbout = $state(false);
	let tsflowVersion = $state('...');
	let uptimeSeconds = $state<number | null>(null);
	const uptimeFormatted = $derived(uptimeSeconds !== null ? formatDuration(uptimeSeconds) : '...');

	async function fetchHealth() {
		try {
			const res = await fetch('/api/health');
			if (res.ok) {
				const data = await res.json();
				if (data.version) tsflowVersion = data.version;
				if (data.uptime) uptimeSeconds = Math.floor(data.uptime);
			}
		} catch (e) {
			console.error('Failed to fetch health info:', e);
		}
	}

	$effect(() => {
		let interval: ReturnType<typeof setInterval>;
		if (showAbout) {
			fetchHealth();
			interval = setInterval(() => {
				if (uptimeSeconds !== null) uptimeSeconds++;
			}, 1000);
		}
		return () => {
			if (interval) clearInterval(interval);
		};
	});

	function handleCloseAbout(e: MouseEvent) {
		if (showAbout && !(e.target as Element).closest('.about-flyout-container')) {
			showAbout = false;
		}
	}

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
	const isTrafficPage = $derived(currentPath === '/');

	const primaryNav = [
		{ href: '/', label: 'Traffic', icon: Network },
		{ href: '/analytics', label: 'Analytics', icon: BarChart3 },
		{ href: '/policy', label: 'Policy', icon: Shield }
	];

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
		return protoTotal > 0
			? protoTotal
			: $statsSummary.virtualBytes + $statsSummary.exitBytes + $statsSummary.subnetBytes + $statsSummary.physicalBytes;
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

<svelte:window onclick={handleCloseAbout} />

<header class="relative z-30 flex h-12 items-center justify-between gap-2 border-b border-border bg-card px-2 sm:h-14 sm:px-4">
	<!-- Left section: Logo + primary navigation -->
	<div class="flex min-w-0 items-center gap-2 sm:gap-3">
		<div class="relative about-flyout-container">
			<button
				onclick={() => (showAbout = !showAbout)}
				class="flex items-center gap-1.5 rounded-md px-1.5 py-1.5 transition-colors hover:bg-secondary sm:gap-2"
				title="About TSFlow"
				aria-label="About TSFlow"
			>
				<Activity class="h-4 w-4 text-primary sm:h-5 sm:w-5" />
				<h1 class="text-base font-semibold sm:text-lg">TSFlow</h1>
			</button>

			{#if showAbout}
				<div
					transition:fly={{ y: -5, duration: 150 }}
					class="absolute top-full left-0 z-50 mt-2 w-64 rounded-lg border border-border bg-popover p-4 text-popover-foreground shadow-xl backdrop-blur-sm"
				>
					<div class="mb-3 flex items-center gap-2">
						<Activity class="h-5 w-5 text-primary" />
						<h2 class="font-semibold text-foreground">TSFlow</h2>
						<span class="rounded bg-secondary px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground"
							>{tsflowVersion.startsWith('v') ? tsflowVersion : `v${tsflowVersion}`}</span
						>
					</div>

					<div class="space-y-2.5 text-sm">
						<a
							href="https://github.com/rajsinghtech/tsflow/releases"
							target="_blank"
							rel="noopener noreferrer"
							class="flex items-center justify-between text-primary hover:underline"
						>
							GitHub Releases
							<ExternalLink class="h-3.5 w-3.5" />
						</a>
						<a
							href="https://github.com/rajsinghtech/tsflow#readme"
							target="_blank"
							rel="noopener noreferrer"
							class="flex items-center justify-between text-primary hover:underline"
						>
							Documentation
							<ExternalLink class="h-3.5 w-3.5" />
						</a>
					</div>

					<div class="mt-4 border-t border-border pt-3">
						<div class="flex items-center justify-between text-[11px] text-muted-foreground">
							<span>Uptime</span>
							<span class="font-mono">{uptimeFormatted}</span>
						</div>
					</div>
				</div>
			{/if}
		</div>

		<!-- Navigation -->
		<nav class="flex min-w-0 items-center rounded-md border border-border bg-muted/30 p-0.5" aria-label="Primary">
			{#each primaryNav as item}
				{@const Icon = item.icon}
				{@const active = currentPath === item.href}
				<a
					href={item.href}
					aria-current={active ? 'page' : undefined}
					aria-label={item.label}
					class="flex min-h-8 items-center gap-1.5 rounded px-2 text-sm text-muted-foreground transition-colors hover:bg-background hover:text-foreground sm:px-3"
					class:bg-background={active}
					class:text-foreground={active}
					class:shadow-sm={active}
				>
					<Icon class="h-4 w-4 shrink-0" />
					<span class="hidden sm:inline">{item.label}</span>
				</a>
			{/each}
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
		{#if isTrafficPage}
			<button
				onclick={handleFilterToggle}
				class="flex min-h-9 min-w-9 items-center justify-center rounded-md border border-transparent p-2 hover:border-border hover:bg-secondary"
				title="Toggle filters"
				aria-label="Toggle filters"
			>
				<PanelLeft class="h-4 w-4" />
			</button>
		{/if}

		<button
			onclick={() => toggleAutoRefresh()}
			class="flex min-h-9 min-w-9 items-center justify-center rounded-md border border-transparent p-2 hover:border-border hover:bg-secondary"
			title={$isAutoRefreshing ? 'Pause auto-refresh (P)' : 'Resume auto-refresh (P)'}
			aria-label={$isAutoRefreshing ? 'Pause auto-refresh' : 'Resume auto-refresh'}
		>
			{#if $isAutoRefreshing}
				<Pause class="h-4 w-4" />
			{:else}
				<Play class="h-4 w-4" />
			{/if}
		</button>

		<button
			onclick={handleRefresh}
			class="flex min-h-9 min-w-9 items-center justify-center gap-2 rounded-md border border-border px-2.5 py-2 hover:bg-secondary disabled:cursor-not-allowed disabled:opacity-60 sm:px-3"
			disabled={isRefreshing}
			title="Refresh now (R)"
			aria-label="Refresh data"
		>
			<RefreshCw class="h-4 w-4 {isRefreshing ? 'animate-spin' : ''}" />
			<span class="hidden text-sm sm:inline">Refresh</span>
		</button>

		<button
			onclick={cycleTheme}
			class="flex min-h-9 min-w-9 items-center justify-center rounded-md border border-transparent p-2 hover:border-border hover:bg-secondary"
			title="Theme: {getThemeLabel($themeStore)} (click to cycle)"
			aria-label="Theme: {getThemeLabel($themeStore)}"
		>
			<ThemeIcon class="h-4 w-4" />
		</button>

		<button
			onclick={() => uiStore.toggleLogViewer()}
			class="flex min-h-9 min-w-9 items-center justify-center rounded-md border border-transparent p-2 hover:border-border hover:bg-secondary"
			title="Toggle log viewer"
			aria-label="Toggle log viewer"
		>
			<ScrollText class="h-4 w-4" />
		</button>

		<button
			onclick={() => (showAbout = !showAbout)}
			class="hidden min-h-9 min-w-9 items-center justify-center rounded-md border border-transparent p-2 hover:border-border hover:bg-secondary sm:flex"
			title="About TSFlow"
			aria-label="About TSFlow"
		>
			<Info class="h-4 w-4" />
		</button>
	</div>
</header>
