<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { Activity, Network, Link, ArrowUpDown, Loader2 } from 'lucide-svelte';
	import Header from '$lib/components/layout/Header.svelte';
	import DonutChart from '$lib/components/charts/DonutChart.svelte';
	import BarChart from '$lib/components/charts/BarChart.svelte';
	import StatCard from '$lib/components/charts/StatCard.svelte';
	import {
		startStatsRefresh,
		stopStatsRefresh,
		statsSummary,
		statsBuckets,
		topTalkers,
		topPairs,
		topPorts,
		statsLoading,
		statsError,
		queryTimeWindow,
		timeRangeStore,
		hasHistoricalData,
		dataSourceStore
	} from '$lib/stores';
	import { formatBytes } from '$lib/utils';
	import { getPortLabel } from '$lib/utils/protocol';

	onMount(() => {
		// Analytics is most useful over at least 1h; nudge the default up from 5m
		const SHORT_RANGES = new Set(['1m', '5m', '15m', '30m']);
		if (SHORT_RANGES.has($timeRangeStore.selected)) {
			timeRangeStore.setPreset('1h');
		}
		if (!$dataSourceStore.dataRange) {
			dataSourceStore.fetchDataRange();
		}
		startStatsRefresh(60_000);
	});

	onDestroy(() => {
		stopStatsRefresh();
	});

	type TalkerField = 'totalBytes' | 'txBytes' | 'rxBytes';
	type PairField = 'totalBytes' | 'flowCount';
	let talkerSort: TalkerField = $state('totalBytes');
	let talkerSortDir: 'asc' | 'desc' = $state('desc');
	let pairSort: PairField = $state('totalBytes');
	let pairSortDir: 'asc' | 'desc' = $state('desc');

	function toggleTalkerSort(field: TalkerField) {
		if (talkerSort === field) {
			talkerSortDir = talkerSortDir === 'desc' ? 'asc' : 'desc';
		} else {
			talkerSort = field;
			talkerSortDir = 'desc';
		}
	}

	function togglePairSort(field: PairField) {
		if (pairSort === field) {
			pairSortDir = pairSortDir === 'desc' ? 'asc' : 'desc';
		} else {
			pairSort = field;
			pairSortDir = 'desc';
		}
	}

	function sortArrow(active: boolean, dir: 'asc' | 'desc'): string {
		if (!active) return '';
		return dir === 'desc' ? ' \u25BE' : ' \u25B4';
	}

	const sortedTalkers = $derived.by(() => {
		const list = [...$topTalkers];
		const mul = talkerSortDir === 'desc' ? -1 : 1;
		return list.sort((a, b) => mul * (a[talkerSort] - b[talkerSort]));
	});

	const sortedPairs = $derived.by(() => {
		const list = [...$topPairs];
		const mul = pairSortDir === 'desc' ? -1 : 1;
		return list.sort((a, b) => mul * (a[pairSort] - b[pairSort]));
	});

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
		return $statsSummary.virtualBytes + $statsSummary.subnetBytes;
	});

	const timeWindowLabel = $derived.by(() => {
		const tw = $queryTimeWindow;
		const diffMs = tw.end.getTime() - tw.start.getTime();
		const mins = Math.round(diffMs / 60_000);
		if (mins < 60) return `Last ${mins} min`;
		const hrs = Math.round(mins / 60);
		if (hrs < 24) return `Last ${hrs}h`;
		const days = Math.round(hrs / 24);
		return `Last ${days}d`;
	});

	// Sparkline data derived from stats buckets
	const trafficSparkline = $derived($statsBuckets.map((b) => b.tcpBytes + b.udpBytes + b.otherProtoBytes));
	const flowsSparkline = $derived($statsBuckets.map((b) => b.totalFlows));
	const pairsSparkline = $derived($statsBuckets.map((b) => b.uniquePairs));

	function nodeLabel(id: string, displayName?: string): string {
		if (displayName) return displayName;
		if (/^\d{10,}$/.test(id)) return id.slice(0, 8) + '\u2026';
		return id;
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
			{#if $statsSummary && $statsSummary.totalFlows === 0 && $hasHistoricalData && $dataSourceStore.mode !== 'historical'}
				<div class="mb-4 rounded-lg border border-border bg-card px-4 py-3 text-sm text-muted-foreground sm:mb-6">
					No traffic data in the selected window.
					Switch to <button
						class="underline hover:text-foreground"
						onclick={() => dataSourceStore.setMode('historical')}
					>Historical mode</button> to browse stored data.
				</div>
			{/if}
			<!-- Overview Cards -->
			<div class="mb-4 grid grid-cols-2 gap-2 sm:mb-6 sm:gap-4 lg:grid-cols-4">
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
					value={$topTalkers.length.toString()}
					subtitle="With traffic"
				>
					{#snippet icon()}<Network class="h-4 w-4" />{/snippet}
				</StatCard>
			</div>

			<!-- Distribution Charts -->
			<div class="mb-4 grid grid-cols-1 gap-3 sm:mb-6 sm:gap-6 lg:grid-cols-2">
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
			</div>

			<!-- Rankings -->
			<div class="mb-4 grid grid-cols-1 gap-3 sm:mb-6 sm:gap-6 lg:grid-cols-2">
				<!-- Top Talkers -->
				<div class="rounded-lg border border-border bg-card p-3 sm:p-4">
					<h3 class="mb-3 text-sm font-medium text-muted-foreground">Top Talkers</h3>

					{#if sortedTalkers.length === 0}
						<div class="flex flex-col items-center justify-center py-8 text-center">
							<Network class="mb-2 h-8 w-8 text-muted-foreground/30" />
							<p class="text-sm text-muted-foreground">No device traffic recorded yet</p>
							<p class="mt-1 text-xs text-muted-foreground/60">Traffic data will appear here once devices start communicating</p>
						</div>
					{:else}
					<!-- Desktop/Tablet table -->
					<div class="hidden overflow-x-auto sm:block">
						<table class="w-full text-sm">
							<thead>
								<tr class="border-b border-border text-left text-muted-foreground">
									<th class="pb-2 pr-4">#</th>
									<th class="pb-2 pr-4">Device</th>
									<th class="pb-2 pr-4 text-muted-foreground">Owner</th>
									<th
										class="cursor-pointer select-none pb-2 pr-4 text-right transition-colors hover:text-foreground"
										onclick={() => toggleTalkerSort('txBytes')}
										aria-sort={talkerSort === 'txBytes' ? (talkerSortDir === 'desc' ? 'descending' : 'ascending') : 'none'}
										aria-label="Sort by transmitted bytes"
									>
										TX{sortArrow(talkerSort === 'txBytes', talkerSortDir)}
									</th>
									<th
										class="cursor-pointer select-none pb-2 pr-4 text-right transition-colors hover:text-foreground"
										onclick={() => toggleTalkerSort('rxBytes')}
										aria-sort={talkerSort === 'rxBytes' ? (talkerSortDir === 'desc' ? 'descending' : 'ascending') : 'none'}
										aria-label="Sort by received bytes"
									>
										RX{sortArrow(talkerSort === 'rxBytes', talkerSortDir)}
									</th>
									<th
										class="cursor-pointer select-none pb-2 text-right transition-colors hover:text-foreground"
										onclick={() => toggleTalkerSort('totalBytes')}
										aria-sort={talkerSort === 'totalBytes' ? (talkerSortDir === 'desc' ? 'descending' : 'ascending') : 'none'}
										aria-label="Sort by total bytes"
									>
										Total{sortArrow(talkerSort === 'totalBytes', talkerSortDir)}
									</th>
								</tr>
							</thead>
							<tbody>
								{#each sortedTalkers as talker, i}
									<tr class="border-b border-border/50 transition-colors hover:bg-secondary/50">
										<td class="py-1.5 pr-4 text-muted-foreground">{i + 1}</td>
										<td class="max-w-[180px] truncate py-1.5 pr-4" title={talker.nodeId}>
											{#if talker.displayName}
												<span class="font-medium">{talker.displayName}</span>
											{:else}
												<span class="font-mono text-xs text-muted-foreground">
													{nodeLabel(talker.nodeId)}
												</span>
											{/if}
										</td>
										<td class="max-w-[160px] truncate py-1.5 pr-4 text-xs text-muted-foreground" title={talker.owner ?? ''}>
											{talker.owner ?? '—'}
										</td>
										<td class="py-1.5 pr-4 text-right tabular-nums"
											>{formatBytes(talker.txBytes)}</td
										>
										<td class="py-1.5 pr-4 text-right tabular-nums"
											>{formatBytes(talker.rxBytes)}</td
										>
										<td class="py-1.5 text-right font-medium tabular-nums"
											>{formatBytes(talker.totalBytes)}</td
										>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>

					<!-- Mobile card view -->
					<div class="divide-y divide-border/50 sm:hidden">
						{#each sortedTalkers as talker, i}
							<div class="py-2">
								<div class="flex items-center justify-between">
									<div class="flex items-center gap-2">
										<span class="text-xs text-muted-foreground">{i + 1}.</span>
										{#if talker.displayName}
											<span class="text-sm font-medium">{talker.displayName}</span>
										{:else}
											<span class="font-mono text-xs text-muted-foreground">
												{nodeLabel(talker.nodeId)}
											</span>
										{/if}
									</div>
									<span class="text-sm font-medium tabular-nums">{formatBytes(talker.totalBytes)}</span>
								</div>
								<div class="mt-0.5 flex gap-3 pl-5 text-xs text-muted-foreground">
									<span class="tabular-nums">TX {formatBytes(talker.txBytes)}</span>
									<span class="tabular-nums">RX {formatBytes(talker.rxBytes)}</span>
								</div>
								{#if talker.owner}
									<div class="mt-0.5 truncate pl-5 text-xs text-muted-foreground/70">{talker.owner}</div>
								{/if}
							</div>
						{/each}
					</div>
					{/if}
				</div>

				<!-- Top Pairs -->
				<div class="rounded-lg border border-border bg-card p-3 sm:p-4">
					<h3 class="mb-3 text-sm font-medium text-muted-foreground">Top Pairs</h3>

					{#if sortedPairs.length === 0}
						<div class="flex flex-col items-center justify-center py-8 text-center">
							<Link class="mb-2 h-8 w-8 text-muted-foreground/30" />
							<p class="text-sm text-muted-foreground">No communication pairs detected</p>
							<p class="mt-1 text-xs text-muted-foreground/60">Pairs will appear once traffic flows between devices</p>
						</div>
					{:else}
					<!-- Desktop/Tablet table -->
					<div class="hidden overflow-x-auto sm:block">
						<table class="w-full text-sm">
							<thead>
								<tr class="border-b border-border text-left text-muted-foreground">
									<th class="pb-2 pr-4">#</th>
									<th class="pb-2 pr-4">Source</th>
									<th class="pb-2 pr-4">Destination</th>
									<th
										class="cursor-pointer select-none pb-2 pr-4 text-right transition-colors hover:text-foreground"
										onclick={() => togglePairSort('totalBytes')}
										aria-sort={pairSort === 'totalBytes' ? (pairSortDir === 'desc' ? 'descending' : 'ascending') : 'none'}
										aria-label="Sort by traffic volume"
									>
										Traffic{sortArrow(pairSort === 'totalBytes', pairSortDir)}
									</th>
									<th
										class="cursor-pointer select-none pb-2 text-right transition-colors hover:text-foreground"
										onclick={() => togglePairSort('flowCount')}
										aria-sort={pairSort === 'flowCount' ? (pairSortDir === 'desc' ? 'descending' : 'ascending') : 'none'}
										aria-label="Sort by flow count"
									>
										Flows{sortArrow(pairSort === 'flowCount', pairSortDir)}
									</th>
								</tr>
							</thead>
							<tbody>
								{#each sortedPairs as pair, i}
									<tr class="border-b border-border/50 transition-colors hover:bg-secondary/50">
										<td class="py-1.5 pr-4 text-muted-foreground">{i + 1}</td>
										<td class="max-w-[140px] py-1.5 pr-4" title={pair.srcNodeId}>
											<div class="truncate">
												{#if pair.srcDisplayName}
													<span class="font-medium">{pair.srcDisplayName}</span>
												{:else}
													<span class="font-mono text-xs text-muted-foreground">
														{nodeLabel(pair.srcNodeId)}
													</span>
												{/if}
											</div>
											{#if pair.srcOwner}
												<div class="truncate text-xs text-muted-foreground/70">{pair.srcOwner}</div>
											{/if}
										</td>
										<td class="max-w-[140px] py-1.5 pr-4" title={pair.dstNodeId}>
											<div class="truncate">
												{#if pair.dstDisplayName}
													<span class="font-medium">{pair.dstDisplayName}</span>
												{:else}
													<span class="font-mono text-xs text-muted-foreground">
														{nodeLabel(pair.dstNodeId)}
													</span>
												{/if}
											</div>
											{#if pair.dstOwner}
												<div class="truncate text-xs text-muted-foreground/70">{pair.dstOwner}</div>
											{/if}
										</td>
										<td class="py-1.5 pr-4 text-right font-medium tabular-nums"
											>{formatBytes(pair.totalBytes)}</td
										>
										<td class="py-1.5 text-right tabular-nums"
											>{pair.flowCount.toLocaleString()}</td
										>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>

					<!-- Mobile card view -->
					<div class="divide-y divide-border/50 sm:hidden">
						{#each sortedPairs as pair, i}
							<div class="py-2">
								<div class="flex items-center justify-between">
									<span class="text-xs text-muted-foreground">{i + 1}.</span>
									<span class="text-sm font-medium tabular-nums">{formatBytes(pair.totalBytes)}</span>
								</div>
								<div class="mt-0.5 flex items-center gap-1 text-xs">
									<span class="truncate">
										{#if pair.srcDisplayName}
											<span class="font-medium">{pair.srcDisplayName}</span>
										{:else}
											<span class="font-mono text-[10px] text-muted-foreground">{nodeLabel(pair.srcNodeId)}</span>
										{/if}
									</span>
									<span class="shrink-0 text-muted-foreground">&rarr;</span>
									<span class="truncate">
										{#if pair.dstDisplayName}
											<span class="font-medium">{pair.dstDisplayName}</span>
										{:else}
											<span class="font-mono text-[10px] text-muted-foreground">{nodeLabel(pair.dstNodeId)}</span>
										{/if}
									</span>
								</div>
								{#if pair.srcOwner || pair.dstOwner}
									<div class="mt-0.5 flex items-center gap-1 text-[10px] text-muted-foreground/70">
										<span class="truncate">{pair.srcOwner ?? '—'}</span>
										<span class="shrink-0">&rarr;</span>
										<span class="truncate">{pair.dstOwner ?? '—'}</span>
									</div>
								{/if}
								<div class="mt-0.5 text-[10px] text-muted-foreground">
									{pair.flowCount.toLocaleString()} flows
								</div>
							</div>
						{/each}
					</div>
					{/if}
				</div>
			</div>

			<!-- Top Ports -->
			<div class="rounded-lg border border-border bg-card p-3 sm:p-4">
				<h3 class="mb-3 text-sm font-medium text-muted-foreground">Top Ports</h3>
				{#if portBars.length > 0}
					<BarChart bars={portBars} height={400} />
				{:else}
					<p class="text-sm text-muted-foreground">No port data available yet</p>
				{/if}
			</div>
		{/if}
	</main>
</div>
