<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { get } from 'svelte/store';
	import { ArrowUpDown, CalendarClock, Link, Loader2, Network, RefreshCw } from 'lucide-svelte';
	import Header from '#lib/components/layout/Header.svelte';
	import TimelineSlider from '#lib/components/timeline/TimelineSlider.svelte';
	import { pageStep, rankNodeLabel, rankPageLabel } from '#lib/analytics/rank-query';
	import type { RankSort } from '#lib/analytics/rank-query';
	import {
		dataSourceStore,
		hasStoredData,
		loadRankedPairs,
		loadRankedTalkers,
		loadRankings,
		queryTimeWindow,
		rankSort,
		rankedPairs,
		rankedTalkers,
		setRankSort,
		startRankingsRefresh,
		stopRankingsRefresh
	} from '#lib/stores';
	import { formatBytes } from '#lib/utils';

	let showWindowControls = $state(false);

	onMount(() => {
		let cancelled = false;

		async function bootstrap() {
			const [range] = await Promise.all([
				dataSourceStore.fetchDataRange(),
				dataSourceStore.fetchPollerStatus()
			]);
			if (cancelled) return;
			// Keep a window the graph already selected. Otherwise use the same
			// latest stored window the graph opens with.
			const selected = get(dataSourceStore);
			if ((!selected.selectedStart || !selected.selectedEnd) && range?.count) {
				dataSourceStore.showLatestWindow(range);
			}
			if (!cancelled) startRankingsRefresh(60_000);
		}

		void bootstrap();
		return () => {
			cancelled = true;
		};
	});

	onDestroy(() => {
		stopRankingsRefresh();
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

	function sortArrow(active: boolean): string {
		return active ? ' \u25BE' : '';
	}

	function showLatestStoredWindow() {
		dataSourceStore.showLatestWindow();
		void loadRankings(true);
	}

	function onWindowChange() {
		void loadRankings(true);
	}
</script>

{#snippet nodeName(hostname: string, nodeId: string)}
	{@const label = rankNodeLabel(hostname, nodeId)}
	{#if label.mono}
		<span class="font-mono text-xs text-muted-foreground" title={nodeId}>{label.text}</span>
	{:else}
		<span class="font-medium" title={nodeId}>{label.text}</span>
	{/if}
{/snippet}

{#snippet metricHeader(label: string, sort: RankSort, pad: boolean)}
	<th
		class="cursor-pointer select-none pb-2 text-right transition-colors hover:text-foreground {pad ? 'pr-4' : ''}"
		onclick={() => setRankSort(sort)}
		aria-sort={$rankSort === sort ? 'descending' : 'none'}
	>
		{label}{sortArrow($rankSort === sort)}
	</th>
{/snippet}

{#snippet pager(
	kind: string,
	offset: number,
	count: number,
	limit: number,
	hasMore: boolean,
	loading: boolean,
	onPage: (nextOffset: number) => void
)}
	{@const prev = pageStep(offset, limit, -1, hasMore)}
	{@const next = pageStep(offset, limit, 1, hasMore)}
	<div class="flex items-center gap-2">
		<p class="text-xs text-muted-foreground tabular-nums">{rankPageLabel(offset, count)}</p>
		<div class="flex gap-1">
			<button
				type="button"
				class="min-h-7 rounded-md border border-border px-2 text-xs hover:bg-secondary disabled:cursor-not-allowed disabled:opacity-40"
				disabled={prev === null || loading}
				aria-label={`Previous ${kind} page`}
				onclick={() => prev !== null && onPage(prev)}
			>
				Prev
			</button>
			<button
				type="button"
				class="min-h-7 rounded-md border border-border px-2 text-xs hover:bg-secondary disabled:cursor-not-allowed disabled:opacity-40"
				disabled={next === null || loading}
				aria-label={`Next ${kind} page`}
				onclick={() => next !== null && onPage(next)}
			>
				Next
			</button>
		</div>
	</div>
{/snippet}

<div class="flex h-screen flex-col bg-background">
	<Header />

	<main class="min-w-0 flex-1 overflow-y-auto p-3 sm:p-6">
		<div class="mb-4 flex flex-wrap items-center justify-between gap-2 sm:mb-6">
			<div>
				<h2 class="text-base font-semibold">Rankings</h2>
				<p class="text-xs text-muted-foreground">{timeWindowLabel}</p>
			</div>
			<div class="flex items-center gap-2">
				{#if $hasStoredData && !$dataSourceStore.followLatest}
					<button
						type="button"
						class="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-secondary"
						onclick={showLatestStoredWindow}
					>
						Latest
					</button>
				{/if}
				<button
					type="button"
					class="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs hover:bg-secondary"
					onclick={() => loadRankings(false)}
				>
					<RefreshCw class="h-3.5 w-3.5 {$rankedTalkers.loading || $rankedPairs.loading ? 'animate-spin' : ''}" />
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
					aria-expanded={showWindowControls}
					onclick={() => (showWindowControls = !showWindowControls)}
				>
					<CalendarClock class="h-3.5 w-3.5" />
					Window
				</button>

				<div class="h-6 w-px bg-border"></div>

				<div class="flex items-center gap-1 text-xs text-muted-foreground">
					<ArrowUpDown class="h-3.5 w-3.5" />
					<span class="hidden sm:inline">Sort</span>
				</div>
				<div class="flex rounded-md border border-border bg-muted/30 p-0.5" role="group" aria-label="Sort rankings">
					<button
						type="button"
						class="min-h-7 rounded px-2 text-xs text-muted-foreground hover:text-foreground"
						class:bg-background={$rankSort === 'bytes'}
						class:text-foreground={$rankSort === 'bytes'}
						class:shadow-sm={$rankSort === 'bytes'}
						aria-pressed={$rankSort === 'bytes'}
						onclick={() => setRankSort('bytes')}
					>
						Bytes
					</button>
					<button
						type="button"
						class="min-h-7 rounded px-2 text-xs text-muted-foreground hover:text-foreground"
						class:bg-background={$rankSort === 'flows'}
						class:text-foreground={$rankSort === 'flows'}
						class:shadow-sm={$rankSort === 'flows'}
						aria-pressed={$rankSort === 'flows'}
						onclick={() => setRankSort('flows')}
					>
						Flows
					</button>
				</div>
			</div>

			{#if showWindowControls}
				<div class="mt-2 border-t border-border pt-2">
					<TimelineSlider {onWindowChange} />
				</div>
			{/if}
		</div>

		<div class="grid grid-cols-1 items-start gap-3 lg:grid-cols-2">
			<section class="rounded-lg border border-border bg-card p-3 sm:p-4" aria-labelledby="ranked-talkers-heading">
				<div class="mb-3 flex flex-wrap items-center justify-between gap-2">
					<div class="flex items-center gap-2">
						<h3 id="ranked-talkers-heading" class="text-sm font-medium text-muted-foreground">Talkers</h3>
						{#if $rankedTalkers.loading && $rankedTalkers.rows.length > 0}
							<Loader2 class="h-3.5 w-3.5 animate-spin text-muted-foreground" />
						{/if}
					</div>
					{#if $rankedTalkers.count > 0 || $rankedTalkers.offset > 0 || $rankedTalkers.hasMore}
						{@render pager(
							'talkers',
							$rankedTalkers.offset,
							$rankedTalkers.count,
							$rankedTalkers.limit,
							$rankedTalkers.hasMore,
							$rankedTalkers.loading,
							loadRankedTalkers
						)}
					{/if}
				</div>

				{#if $rankedTalkers.error && $rankedTalkers.rows.length > 0}
					<p class="mb-2 text-xs text-destructive">{$rankedTalkers.error}</p>
				{/if}

				{#if $rankedTalkers.loading && $rankedTalkers.rows.length === 0}
					<div class="flex items-center justify-center py-8">
						<Loader2 class="h-6 w-6 animate-spin text-primary" />
					</div>
				{:else if $rankedTalkers.error && $rankedTalkers.rows.length === 0}
					<p class="py-8 text-center text-sm text-destructive">{$rankedTalkers.error}</p>
				{:else if $rankedTalkers.rows.length === 0}
					<div class="flex flex-col items-center justify-center py-8 text-center">
						<Network class="mb-2 h-8 w-8 text-muted-foreground/30" />
						<p class="text-sm text-muted-foreground">No device traffic in this window</p>
					</div>
				{:else}
					<div class="hidden overflow-x-auto sm:block">
						<table class="w-full text-sm">
							<thead>
								<tr class="border-b border-border text-left text-muted-foreground">
									<th class="pb-2 pr-4">#</th>
									<th class="pb-2 pr-4">Device</th>
									<th class="pb-2 pr-4 text-right">TX</th>
									<th class="pb-2 pr-4 text-right">RX</th>
									{@render metricHeader('Total', 'bytes', true)}
									{@render metricHeader('Flows', 'flows', false)}
								</tr>
							</thead>
							<tbody>
								{#each $rankedTalkers.rows as talker, i (talker.nodeId)}
									<tr class="border-b border-border/50 transition-colors hover:bg-secondary/50">
										<td class="py-1.5 pr-4 text-muted-foreground tabular-nums">{$rankedTalkers.offset + i + 1}</td>
										<td class="max-w-[180px] truncate py-1.5 pr-4">
											{@render nodeName(talker.hostname, talker.nodeId)}
										</td>
										<td class="py-1.5 pr-4 text-right tabular-nums">{formatBytes(talker.txBytes)}</td>
										<td class="py-1.5 pr-4 text-right tabular-nums">{formatBytes(talker.rxBytes)}</td>
										<td class="py-1.5 pr-4 text-right font-medium tabular-nums">{formatBytes(talker.totalBytes)}</td>
										<td class="py-1.5 text-right tabular-nums">{talker.flowCount.toLocaleString()}</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>

					<div class="divide-y divide-border/50 sm:hidden">
						{#each $rankedTalkers.rows as talker, i (talker.nodeId)}
							<div class="py-2">
								<div class="flex items-center justify-between gap-2">
									<div class="flex min-w-0 items-center gap-2">
										<span class="text-xs text-muted-foreground tabular-nums">{$rankedTalkers.offset + i + 1}.</span>
										<span class="truncate">{@render nodeName(talker.hostname, talker.nodeId)}</span>
									</div>
									<span class="shrink-0 text-sm font-medium tabular-nums">{formatBytes(talker.totalBytes)}</span>
								</div>
								<div class="mt-0.5 flex gap-3 pl-5 text-xs text-muted-foreground">
									<span class="tabular-nums">TX {formatBytes(talker.txBytes)}</span>
									<span class="tabular-nums">RX {formatBytes(talker.rxBytes)}</span>
									<span class="tabular-nums">{talker.flowCount.toLocaleString()} flows</span>
								</div>
							</div>
						{/each}
					</div>
				{/if}
			</section>

			<section class="rounded-lg border border-border bg-card p-3 sm:p-4" aria-labelledby="ranked-pairs-heading">
				<div class="mb-3 flex flex-wrap items-center justify-between gap-2">
					<div class="flex items-center gap-2">
						<h3 id="ranked-pairs-heading" class="text-sm font-medium text-muted-foreground">Pairs</h3>
						{#if $rankedPairs.loading && $rankedPairs.rows.length > 0}
							<Loader2 class="h-3.5 w-3.5 animate-spin text-muted-foreground" />
						{/if}
					</div>
					{#if $rankedPairs.count > 0 || $rankedPairs.offset > 0 || $rankedPairs.hasMore}
						{@render pager(
							'pairs',
							$rankedPairs.offset,
							$rankedPairs.count,
							$rankedPairs.limit,
							$rankedPairs.hasMore,
							$rankedPairs.loading,
							loadRankedPairs
						)}
					{/if}
				</div>

				{#if $rankedPairs.error && $rankedPairs.rows.length > 0}
					<p class="mb-2 text-xs text-destructive">{$rankedPairs.error}</p>
				{/if}

				{#if $rankedPairs.loading && $rankedPairs.rows.length === 0}
					<div class="flex items-center justify-center py-8">
						<Loader2 class="h-6 w-6 animate-spin text-primary" />
					</div>
				{:else if $rankedPairs.error && $rankedPairs.rows.length === 0}
					<p class="py-8 text-center text-sm text-destructive">{$rankedPairs.error}</p>
				{:else if $rankedPairs.rows.length === 0}
					<div class="flex flex-col items-center justify-center py-8 text-center">
						<Link class="mb-2 h-8 w-8 text-muted-foreground/30" />
						<p class="text-sm text-muted-foreground">No communication pairs in this window</p>
					</div>
				{:else}
					<div class="hidden overflow-x-auto sm:block">
						<table class="w-full text-sm">
							<thead>
								<tr class="border-b border-border text-left text-muted-foreground">
									<th class="pb-2 pr-4">#</th>
									<th class="pb-2 pr-4">Source</th>
									<th class="pb-2 pr-4">Destination</th>
									{@render metricHeader('Traffic', 'bytes', true)}
									{@render metricHeader('Flows', 'flows', false)}
								</tr>
							</thead>
							<tbody>
								{#each $rankedPairs.rows as pair, i (`${pair.srcNodeId}\0${pair.dstNodeId}`)}
									<tr class="border-b border-border/50 transition-colors hover:bg-secondary/50">
										<td class="py-1.5 pr-4 text-muted-foreground tabular-nums">{$rankedPairs.offset + i + 1}</td>
										<td class="max-w-[160px] truncate py-1.5 pr-4">
											{@render nodeName(pair.srcHostname, pair.srcNodeId)}
										</td>
										<td class="max-w-[160px] truncate py-1.5 pr-4">
											{@render nodeName(pair.dstHostname, pair.dstNodeId)}
										</td>
										<td class="py-1.5 pr-4 text-right font-medium tabular-nums">{formatBytes(pair.totalBytes)}</td>
										<td class="py-1.5 text-right tabular-nums">{pair.flowCount.toLocaleString()}</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>

					<div class="divide-y divide-border/50 sm:hidden">
						{#each $rankedPairs.rows as pair, i (`${pair.srcNodeId}\0${pair.dstNodeId}`)}
							<div class="py-2">
								<div class="flex items-center justify-between gap-2">
									<span class="text-xs text-muted-foreground tabular-nums">{$rankedPairs.offset + i + 1}.</span>
									<span class="text-sm font-medium tabular-nums">{formatBytes(pair.totalBytes)}</span>
								</div>
								<div class="mt-0.5 flex min-w-0 items-center gap-1 text-xs">
									<span class="truncate">{@render nodeName(pair.srcHostname, pair.srcNodeId)}</span>
									<span class="shrink-0 text-muted-foreground">&rarr;</span>
									<span class="truncate">{@render nodeName(pair.dstHostname, pair.dstNodeId)}</span>
								</div>
								<div class="mt-0.5 text-[10px] text-muted-foreground">
									{pair.flowCount.toLocaleString()} flows
								</div>
							</div>
						{/each}
					</div>
				{/if}
			</section>
		</div>
	</main>
</div>
