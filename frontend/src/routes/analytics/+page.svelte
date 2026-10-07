<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { Activity, Network, Link, ArrowUpDown, Loader2, RefreshCw, CalendarClock, SlidersHorizontal } from 'lucide-svelte';
	import RankedTables from '#lib/components/analytics/RankedTables.svelte';
	import Header from '#lib/components/layout/Header.svelte';
	import DonutChart from '#lib/components/charts/DonutChart.svelte';
	import BarChart from '#lib/components/charts/BarChart.svelte';
	import StatCard from '#lib/components/charts/StatCard.svelte';
	import TimelineSlider from '#lib/components/timeline/TimelineSlider.svelte';
	import {
		startStatsRefresh,
		stopStatsRefresh,
		loadStats,
		statsSummary,
		statsBuckets,
		topPorts,
		statsLoading,
		statsError,
		queryTimeWindow,
		hasStoredData,
		dataSourceStore,
		filterStore,
		loadRankings,
		startRankingsRefresh,
		stopRankingsRefresh
	} from '#lib/stores';
	import { formatBytes, resolveActiveNodeCount } from '#lib/utils';
	import { getPortLabel } from '#lib/utils/protocol';
	import type { TrafficType } from '#lib/types';

	onMount(() => {
		let cancelled = false;

		async function bootstrapAnalytics() {
			const [range] = await Promise.all([
				dataSourceStore.fetchDataRange(),
				dataSourceStore.fetchPollerStatus()
			]);
			if (cancelled) return;
			if (range?.count) {
				dataSourceStore.showLatestWindow(range);
			}
			startStatsRefresh(60_000);
			startRankingsRefresh(60_000);
		}

		bootstrapAnalytics();
		return () => {
			cancelled = true;
		};
	});

	onDestroy(() => {
		stopStatsRefresh();
		stopRankingsRefresh();
	});

	let showWindowControls = $state(false);
	const trafficTypes: { value: TrafficType; label: string; colorClass: string }[] = [
		{ value: 'virtual', label: 'Virtual', colorClass: 'bg-blue-500' },
		{ value: 'subnet', label: 'Subnet', colorClass: 'bg-green-500' },
		{ value: 'exit', label: 'Exit Node', colorClass: 'bg-purple-500' },
		{ value: 'physical', label: 'Physical', colorClass: 'bg-amber-500' }
	];
	const selectedTrafficTypes = $derived(new Set($filterStore.trafficTypes));

	function setAnalyticsTrafficTypes(types: TrafficType[]) {
		filterStore.setTrafficTypes(types);
		reloadAll(true);
	}

	function toggleTrafficType(type: TrafficType) {
		const next = new Set($filterStore.trafficTypes);
		if (next.has(type)) {
			next.delete(type);
		} else {
			next.add(type);
		}
		setAnalyticsTrafficTypes([...next]);
	}

	function selectAllTrafficTypes() {
		setAnalyticsTrafficTypes(trafficTypes.map((t) => t.value));
	}

	function clearAllTrafficTypes() {
		setAnalyticsTrafficTypes([]);
	}

	// Reload the overview and the ranked tables. Window and filter changes
	// return the tables to their first page; a refresh keeps the page.
	function reloadAll(resetPages: boolean) {
		loadStats();
		void loadRankings(resetPages);
	}

	const protoSegments = $derived.by(() => {
		const s = $statsSummary;
		if (!s) return [];
		return [
			{ label: 'TCP', value: s.tcpBytes, color: 'var(--color-primary)' },
			{ label: 'UDP', value: s.udpBytes, color: 'var(--color-traffic-subnet)' },
			{ label: 'Other', value: s.otherProtoBytes, color: 'var(--color-traffic-physical)' }
		];
	});

	const trafficTypeSegments = $derived.by(() => {
		const s = $statsSummary;
		if (!s) return [];
		return [
			{ label: 'Virtual', value: s.virtualBytes, color: 'var(--color-traffic-virtual)' },
			{ label: 'Exit Node', value: s.exitBytes, color: 'var(--color-traffic-exit)' },
			{ label: 'Subnet', value: s.subnetBytes, color: 'var(--color-traffic-subnet)' },
			{ label: 'Physical', value: s.physicalBytes, color: 'var(--color-traffic-physical)' }
		];
	});

	const portBars = $derived.by(() => {
		return $topPorts.map((p) => ({
			label: getPortLabel(p.port, p.proto),
			value: p.bytes,
			color:
				p.proto === 6
					? 'var(--color-primary)'
					: p.proto === 17
						? 'var(--color-traffic-subnet)'
						: 'var(--color-traffic-physical)'
		}));
	});

	const totalBytes = $derived.by(() => {
		if (!$statsSummary) return 0;
		// Use protocol breakdown when available, otherwise fall back to traffic type totals
		const protoTotal = $statsSummary.tcpBytes + $statsSummary.udpBytes + $statsSummary.otherProtoBytes;
		if (protoTotal > 0) return protoTotal;
		return $statsSummary.virtualBytes + $statsSummary.exitBytes + $statsSummary.subnetBytes + $statsSummary.physicalBytes;
	});

	const timeWindowLabel = $derived.by(() => {
		const tw = $queryTimeWindow;
		const start = tw.start.toLocaleString(undefined, {
			month: 'short',
			day: 'numeric',
			hour: '2-digit',
			minute: '2-digit'
		});
		const end = tw.end.toLocaleString(undefined, {
			month: 'short',
			day: 'numeric',
			hour: '2-digit',
			minute: '2-digit'
		});
		return `${start} - ${end}`;
	});

	// Sparkline data derived from stats buckets
	const trafficSparkline = $derived($statsBuckets.map((b) => b.tcpBytes + b.udpBytes + b.otherProtoBytes));
	const flowsSparkline = $derived($statsBuckets.map((b) => b.totalFlows));
	const pairsSparkline = $derived($statsBuckets.map((b) => b.uniquePairs));

	function showLatestStoredWindow() {
		dataSourceStore.showLatestWindow();
		reloadAll(true);
	}
</script>

<div class="flex h-screen flex-col bg-background">
	<Header />

	<main class="flex-1 overflow-y-auto p-3 sm:p-6">
		{#if $statsLoading && !$statsSummary}
			<div class="flex h-full items-center justify-center">
				<Loader2 class="h-8 w-8 animate-spin text-primary" />
			</div>
		{:else if $statsError && !$statsSummary}
			<div class="flex h-full items-center justify-center text-destructive">
				{$statsError}
			</div>
		{:else}
			{#if $statsSummary && $statsSummary.totalFlows === 0 && $hasStoredData}
				<div class="mb-4 rounded-lg border border-border bg-card px-4 py-3 text-sm text-muted-foreground sm:mb-6">
					No traffic data in the selected window.
					Switch to <button
						class="rounded-md border border-border px-2 py-1 text-xs hover:bg-secondary hover:text-foreground"
						onclick={showLatestStoredWindow}
					>latest stored window</button> to browse stored data.
				</div>
			{/if}
			<div class="mb-4 flex flex-wrap items-center justify-between gap-2 sm:mb-6">
				<div>
					<h2 class="text-base font-semibold">Analytics</h2>
					<p class="text-xs text-muted-foreground">{timeWindowLabel}</p>
				</div>
				<div class="flex items-center gap-2">
					{#if $hasStoredData && !$dataSourceStore.followLatest}
						<button
							class="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-secondary"
							onclick={showLatestStoredWindow}
						>
							Latest
						</button>
					{/if}
					<button
						class="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs hover:bg-secondary"
						onclick={() => reloadAll(false)}
					>
						<RefreshCw class="h-3.5 w-3.5" />
						Refresh
					</button>
				</div>
			</div>

			<div class="sticky top-0 z-20 mb-4 rounded-lg border border-border bg-card/95 p-2 shadow-sm backdrop-blur sm:mb-5">
				<div class="flex flex-wrap items-center gap-2">
					<button
						type="button"
						class="inline-flex min-h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs hover:bg-secondary"
						class:bg-secondary={showWindowControls}
						onclick={() => (showWindowControls = !showWindowControls)}
					>
						<CalendarClock class="h-3.5 w-3.5" />
						Window
					</button>

					<div class="h-6 w-px bg-border"></div>

					<div class="flex items-center gap-1 text-xs text-muted-foreground">
						<SlidersHorizontal class="h-3.5 w-3.5" />
						<span class="hidden sm:inline">Traffic</span>
					</div>

					<div class="flex flex-wrap gap-1">
						{#each trafficTypes as type}
							<button
								type="button"
								onclick={() => toggleTrafficType(type.value)}
								class="inline-flex min-h-7 items-center gap-1.5 rounded-md border px-2 text-xs transition-colors"
								class:border-primary={selectedTrafficTypes.has(type.value)}
								class:bg-secondary={selectedTrafficTypes.has(type.value)}
								class:border-border={!selectedTrafficTypes.has(type.value)}
								class:text-muted-foreground={!selectedTrafficTypes.has(type.value)}
							>
								<span class="h-2 w-2 rounded-full {type.colorClass}"></span>
								{type.label}
							</button>
						{/each}
					</div>

					<div class="ml-auto flex gap-1">
						<button onclick={selectAllTrafficTypes} class="min-h-7 rounded-md border border-border px-2 text-xs hover:bg-secondary">All</button>
						<button onclick={clearAllTrafficTypes} class="min-h-7 rounded-md border border-border px-2 text-xs hover:bg-secondary">
							None
						</button>
					</div>
				</div>

				{#if showWindowControls}
					<div class="mt-2 border-t border-border pt-2">
						<TimelineSlider onWindowChange={() => reloadAll(true)} />
					</div>
				{/if}
			</div>

			<!-- Overview Cards -->
			<div class="mb-4 grid grid-cols-2 gap-2 sm:mb-5 sm:gap-3 lg:grid-cols-4">
				<StatCard label="Total Traffic" value={formatBytes(totalBytes)} subtitle={timeWindowLabel} sparkline={trafficSparkline}>
					{#snippet icon()}<Activity class="h-4 w-4" />{/snippet}
				</StatCard>
				<StatCard
					label="Total Flows"
					value={($statsSummary?.totalFlows ?? 0).toLocaleString()}
					subtitle={timeWindowLabel}
					sparkline={flowsSparkline}
					sparkColor="var(--color-traffic-subnet)"
				>
					{#snippet icon()}<ArrowUpDown class="h-4 w-4" />{/snippet}
				</StatCard>
				<StatCard
					label="Unique Pairs"
					value={($statsSummary?.uniquePairs ?? 0).toLocaleString()}
					subtitle="Device pairs"
					sparkline={pairsSparkline}
					sparkColor="var(--color-traffic-virtual)"
				>
					{#snippet icon()}<Link class="h-4 w-4" />{/snippet}
				</StatCard>
				<StatCard
					label="Active Devices"
					value={resolveActiveNodeCount($statsSummary?.totalNodes).toLocaleString()}
					subtitle="With traffic"
				>
					{#snippet icon()}<Network class="h-4 w-4" />{/snippet}
				</StatCard>
			</div>

			<!-- Distribution Charts -->
			<div class={`mb-4 grid grid-cols-1 gap-3 sm:mb-5 ${portBars.length > 0 ? 'lg:grid-cols-3' : 'lg:grid-cols-2'}`}>
				<div class="rounded-lg border border-border bg-card p-3 sm:p-4">
					<h3 class="mb-3 text-sm font-medium text-muted-foreground">
						Protocol Distribution
					</h3>
					<DonutChart segments={protoSegments} />
				</div>
				<div class="rounded-lg border border-border bg-card p-3 sm:p-4">
					<h3 class="mb-3 text-sm font-medium text-muted-foreground">
						Traffic Type Distribution
					</h3>
					<DonutChart segments={trafficTypeSegments} />
				</div>
				{#if portBars.length > 0}
					<div class="rounded-lg border border-border bg-card p-3 sm:p-4">
						<h3 class="mb-3 text-sm font-medium text-muted-foreground">Top Ports</h3>
						<BarChart bars={portBars} height={260} />
					</div>
				{/if}
			</div>

			<RankedTables />

		{/if}
	</main>
</div>
