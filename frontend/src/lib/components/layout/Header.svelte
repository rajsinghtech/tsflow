<script lang="ts">
	import { ListFilter, Logs, Sun, Moon, Monitor, Network, Link, Activity, BarChart3, Shield, ExternalLink, Waypoints, User, MoreHorizontal } from 'lucide-svelte';
	import { rememberTab, showMeTab } from '#lib/analytics/landing';
	import TailnetSwitcher from './TailnetSwitcher.svelte';
	import TimeControls from '#lib/components/timeline/TimeControls.svelte';
	import { ensureTailnetQuery, hrefWithTailnet } from '#lib/services/tailnet-query';
	import { selectedTailnetId } from '#lib/stores/tailnet-store';
	import { fly } from 'svelte/transition';
	import { page } from '$app/state';
	import { uiStore, networkStats, filteredNodes, lastUpdated, themeStore, statsSummary, viewerStore } from '#lib/stores';
	import { policyGraph } from '#lib/stores/policy-store';
	import { formatBytes, formatDuration, averageBytesPerNode, headerLinkLabel, headerNodeCount, headerShowsStats, headerStat } from '#lib/utils';
	import type { ThemeMode } from '#lib/stores';

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

	// Phones fold the theme and log buttons into this menu.
	let showMore = $state(false);

	function handleCloseAbout(e: MouseEvent) {
		if (showAbout && !(e.target as Element).closest('.about-flyout-container')) {
			showAbout = false;
		}
		if (showMore && !(e.target as Element).closest('.more-menu-container')) {
			showMore = false;
		}
	}

	function handleMenuKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape' && showMore) showMore = false;
	}

	const currentPath = $derived(page.url.pathname);
	const isTrafficPage = $derived(currentPath === '/');

	const baseNav = [
		{ href: '/', label: 'Traffic', icon: Network },
		{ href: '/analytics', label: 'Analytics', icon: BarChart3 },
		{ href: '/new', label: 'New', icon: Waypoints },
		{ href: '/policy', label: 'Policy', icon: Shield }
	];
	const primaryNav = $derived(
		showMeTab($viewerStore?.login) ? [{ href: '/me', label: 'Me', icon: User }, ...baseNav] : baseNav
	);

	// Traffic view uses the graph, which includes every node in that window.
	// Analytics uses the overview active-node count once stats are loaded.
	// The top-talkers list is capped and is not a device count.
	const hasNetworkData = $derived($networkStats.totalNodes > 0);
	const showStats = $derived(headerShowsStats(currentPath, hasNetworkData));
	const analyticsStatsReady = $derived(currentPath === '/analytics' && $statsSummary !== null);
	const useNetworkStats = $derived(!analyticsStatsReady && hasNetworkData);

	const displayNodes = $derived(
		headerNodeCount($networkStats.totalNodes, $statsSummary?.totalNodes, $statsSummary !== null, useNetworkStats)
	);
	const displayFlows = $derived(useNetworkStats ? $networkStats.totalConnections : ($statsSummary?.totalFlows ?? 0));
	const displayBytes = $derived.by(() => {
		if (useNetworkStats) return $networkStats.totalBytes;
		if (!$statsSummary) return 0;
		const protoTotal = $statsSummary.tcpBytes + $statsSummary.udpBytes + $statsSummary.otherProtoBytes;
		return protoTotal > 0
			? protoTotal
			: $statsSummary.virtualBytes + $statsSummary.exitBytes + $statsSummary.subnetBytes + $statsSummary.physicalBytes;
	});

	const avgTrafficPerNode = $derived(averageBytesPerNode(displayBytes, displayNodes));

	// Nothing has loaded yet for this window, e.g. the Policy page opened
	// directly. The totals are unknown, not zero.
	const statsLoaded = $derived($lastUpdated !== null || $statsSummary !== null);

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

	const THEME_ORDER: ThemeMode[] = ['light', 'dark', 'system'];
	const nextThemeLabel = $derived(getThemeLabel(THEME_ORDER[(THEME_ORDER.indexOf($themeStore) + 1) % THEME_ORDER.length]));

	function handleFilterToggle() {
		uiStore.toggleFilters();
	}

	const ThemeIcon = $derived(getThemeIcon($themeStore));

	$effect(() => {
		void ensureTailnetQuery();
	});
</script>

<svelte:window onclick={handleCloseAbout} onkeydown={handleMenuKeydown} />

<div class="relative z-30 border-b border-border bg-card">
<header class="flex h-12 items-center justify-between gap-1 px-1 sm:h-14 sm:gap-2 sm:px-4">
	<!-- Left section: Logo + primary navigation -->
	<div class="flex min-w-0 items-center gap-1 sm:gap-3 lg:shrink-0">
		<div class="relative about-flyout-container shrink-0">
			<button
				onclick={() => (showAbout = !showAbout)}
				class="flex items-center gap-1.5 rounded-md px-1.5 py-1.5 transition-colors hover:bg-secondary pointer-coarse:min-h-11 pointer-coarse:min-w-11 pointer-coarse:justify-center sm:gap-2"
				title="About TSFlow"
				aria-label="About TSFlow"
			>
				<Activity class="h-4 w-4 text-primary sm:h-5 sm:w-5" />
				<h1 class="hidden text-base font-semibold sm:inline sm:text-lg">TSFlow</h1>
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
		<nav class="hidden shrink-0 items-center rounded-md border border-border bg-muted/30 p-0.5 sm:flex" aria-label="Primary">
			{#each primaryNav as item}
				{@const Icon = item.icon}
				{@const active = currentPath === item.href}
				<a
					href={hrefWithTailnet(item.href, $selectedTailnetId)}
					onclick={() => rememberTab(item.href)}
					aria-current={active ? 'page' : undefined}
					aria-label={item.label}
					class="flex min-h-8 items-center gap-1.5 rounded px-1 text-sm text-muted-foreground transition-colors hover:bg-background hover:text-foreground pointer-coarse:min-h-11 sm:px-3"
					class:bg-background={active}
					class:text-foreground={active}
					class:shadow-sm={active}
				>
					<Icon class="h-4 w-4 shrink-0" />
					<span class="hidden sm:inline">{item.label}</span>
				</a>
			{/each}
		</nav>

		<TailnetSwitcher />
	</div>

	<!-- Center section: Network Stats (desktop only) -->
	<!-- Takes the free space between navigation and actions. Items drop out
	     by priority as that space narrows instead of overlapping the nav. -->
	<div class="@container hidden min-w-0 flex-1 lg:block">
		{#if showStats}
			<div class="flex items-center justify-center gap-4 overflow-hidden whitespace-nowrap @2xl:gap-6">
				<div class="flex items-center gap-2">
					<Network class="h-4 w-4 text-muted-foreground" />
					<div class="text-sm">
						<span class="font-semibold">{headerStat(statsLoaded, displayNodes)}</span>
						<span class="text-muted-foreground"> {hasNetworkData ? 'nodes' : 'devices'}</span>
					</div>
				</div>

				<div class="flex items-center gap-2">
					<Link class="h-4 w-4 text-muted-foreground" />
					<div class="text-sm">
						<span class="font-semibold">{headerStat(statsLoaded, displayFlows, (v) => (useNetworkStats ? String(v) : v.toLocaleString()))}</span>
						<span class="text-muted-foreground"> {headerLinkLabel(useNetworkStats)}</span>
					</div>
				</div>

				<div class="hidden h-6 w-px bg-border @sm:block"></div>

				<div class="hidden text-sm @sm:block">
					<span class="text-muted-foreground">Traffic:</span>
					<span class="ml-1 font-semibold text-primary">{headerStat(statsLoaded, displayBytes, formatBytes)}</span>
				</div>

				<div class="hidden text-sm @xl:block">
					<span class="text-muted-foreground">Avg/Node:</span>
					<span class="ml-1 font-semibold">{headerStat(statsLoaded, avgTrafficPerNode, formatBytes)}</span>
				</div>

				{#if peakNode}
					<div class="hidden min-w-0 items-baseline text-sm @3xl:flex" title="{peakNode.displayName} ({peakNode.ip}) - {formatBytes(peakNode.totalBytes)}">
						<span class="text-muted-foreground">Peak:</span>
						<span class="ml-1 max-w-48 truncate font-semibold">{peakNode.displayName}</span>
						<span class="ml-1 text-xs text-muted-foreground">({formatBytes(peakNode.totalBytes)})</span>
					</div>
				{/if}

			</div>
		{/if}
	</div>

	{#if showStats}
		<!-- Compact stats for mobile (<md) -->
		<div class="flex shrink-0 items-center gap-1.5 whitespace-nowrap md:hidden">
			<span class="text-[10px] font-semibold tabular-nums">{headerStat(statsLoaded, displayNodes)}<span class="font-normal text-muted-foreground">n</span></span>
			<span class="text-[10px] font-semibold tabular-nums text-primary">{headerStat(statsLoaded, displayBytes, formatBytes)}</span>
		</div>

		<!-- Compact stats for tablet (md only) -->
		<div class="hidden items-center gap-3 md:flex lg:hidden">
			<div class="text-xs">
				<span class="font-semibold">{headerStat(statsLoaded, displayNodes)}</span>
				<span class="text-muted-foreground"> {hasNetworkData ? 'nodes' : 'devices'}</span>
			</div>
			<div class="text-xs">
				<span class="font-semibold text-primary">{headerStat(statsLoaded, displayBytes, formatBytes)}</span>
			</div>
		</div>
	{/if}

	<!-- Right section: Actions -->
	<div class="flex shrink-0 items-center gap-1 sm:gap-2">
		{#if $viewerStore?.name || $viewerStore?.login}
			<span
				class="hidden max-w-[9rem] truncate text-xs text-muted-foreground sm:inline"
				title={$viewerStore.login || $viewerStore.name}
			>
				{$viewerStore.name || $viewerStore.login}
			</span>
		{/if}
		{#if isTrafficPage}
			<button
				type="button"
				onclick={handleFilterToggle}
				class="flex min-h-8 min-w-8 items-center justify-center gap-1.5 rounded-md border border-transparent p-1.5 text-sm hover:border-border hover:bg-secondary pointer-coarse:min-h-11 pointer-coarse:min-w-11 sm:min-h-9 sm:min-w-9 sm:p-2"
				title="Show or hide filters"
				aria-label="Toggle filters"
			>
				<ListFilter class="h-4 w-4" />
				<span class="hidden xl:inline">Filters</span>
			</button>
		{/if}

		<!-- The icon shows the current theme; the label says so, and the tooltip names the next one. -->
		<button
			type="button"
			onclick={cycleTheme}
			class="hidden min-h-9 min-w-9 items-center justify-center gap-1.5 rounded-md border border-transparent p-2 text-sm hover:border-border hover:bg-secondary pointer-coarse:min-h-11 pointer-coarse:min-w-11 sm:flex"
			title="Theme: {getThemeLabel($themeStore)}. Click for {nextThemeLabel}"
			aria-label="Theme: {getThemeLabel($themeStore)}"
		>
			<ThemeIcon class="h-4 w-4" />
			<span class="hidden xl:inline">{getThemeLabel($themeStore)}</span>
		</button>

		<!-- The log panel only exists on Traffic, so the button only shows there, like the phone menu. -->
		{#if isTrafficPage}
			<button
				type="button"
				onclick={() => uiStore.toggleLogViewer()}
				class="hidden min-h-9 min-w-9 items-center justify-center gap-1.5 rounded-md border border-transparent p-2 text-sm hover:border-border hover:bg-secondary pointer-coarse:min-h-11 pointer-coarse:min-w-11 sm:flex"
				title="Show or hide flow logs"
				aria-label="Toggle log viewer"
			>
				<Logs class="h-4 w-4" />
				<span class="hidden xl:inline">Logs</span>
			</button>
		{/if}

		<div class="more-menu-container relative sm:hidden">
			<button
				type="button"
				onclick={() => (showMore = !showMore)}
				class="flex h-11 w-11 items-center justify-center rounded-md border border-transparent hover:border-border hover:bg-secondary"
				aria-haspopup="menu"
				aria-expanded={showMore}
				aria-label="More"
				title="More"
			>
				<MoreHorizontal class="h-5 w-5" />
			</button>
			{#if showMore}
				<div
					transition:fly={{ y: -5, duration: 150 }}
					class="absolute top-full right-0 z-50 mt-1 w-56 max-w-[calc(100vw-1rem)] rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-xl"
					role="menu"
				>
					{#if $viewerStore?.name || $viewerStore?.login}
						<div class="truncate px-3 py-2 text-xs text-muted-foreground" title={$viewerStore.login || $viewerStore.name}>
							Signed in as {$viewerStore.name || $viewerStore.login}
						</div>
					{/if}
					<button
						type="button"
						role="menuitem"
						onclick={cycleTheme}
						class="flex min-h-11 w-full items-center gap-3 rounded-md px-3 text-left text-sm hover:bg-secondary"
						title="Click for {nextThemeLabel}"
					>
						<ThemeIcon class="h-4 w-4" />
						Theme: {getThemeLabel($themeStore)}
					</button>
					{#if isTrafficPage}
						<button
							type="button"
							role="menuitem"
							onclick={() => {
								showMore = false;
								uiStore.toggleLogViewer();
							}}
							class="flex min-h-11 w-full items-center gap-3 rounded-md px-3 text-left text-sm hover:bg-secondary"
						>
							<Logs class="h-4 w-4" />
							Flow logs
						</button>
					{/if}
				</div>
			{/if}
		</div>
	</div>
</header>
<!-- Phones: the tabs get their own row, with labels and full-size targets. -->
<nav class="grid border-t border-border sm:hidden" style="grid-template-columns: repeat({primaryNav.length}, minmax(0, 1fr))" aria-label="Primary">
	{#each primaryNav as item}
		{@const Icon = item.icon}
		{@const active = currentPath === item.href}
		<a
			href={hrefWithTailnet(item.href, $selectedTailnetId)}
			onclick={() => rememberTab(item.href)}
			aria-current={active ? 'page' : undefined}
			class="flex h-11 items-center justify-center gap-1.5 border-b-2 text-xs text-muted-foreground transition-colors"
			class:border-transparent={!active}
			class:border-primary={active}
			class:text-foreground={active}
			class:font-medium={active}
		>
			<Icon class="h-4 w-4 shrink-0" />
			<span class="truncate">{item.label}</span>
		</a>
	{/each}
</nav>
{#if currentPath !== '/policy'}
	<TimeControls />
{/if}
</div>
