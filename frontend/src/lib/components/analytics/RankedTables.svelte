<script lang="ts">
	// Top talkers and pairs for the Analytics page: one ranked page at a time
	// from /api/analytics/talkers and /api/analytics/pairs, with search, sort,
	// paging, and click-through to the traffic graph over the same window.
	import { onDestroy } from 'svelte';
	import { get } from 'svelte/store';
	import { ArrowUpDown, Link, Loader2, Network, Search, X } from 'lucide-svelte';
	import { goto } from '$app/navigation';
	import { pageStep, rankNodeLabel, rankPageLabel, subnetRouteHint, trafficSearchFor } from '#lib/analytics/rank-query';
	import type { RankSort } from '#lib/analytics/rank-query';
	import {
		dataSourceStore,
		filterStore,
		loadRankedPairs,
		loadRankedTalkers,
		rankSearch,
		rankSort,
		rankedPairs,
		rankedTalkers,
		setRankSearch,
		setRankSort,
		uiStore
	} from '#lib/stores';
	import { formatBytes } from '#lib/utils';

	let searchText = $state(get(rankSearch));
	let searchTimer: ReturnType<typeof setTimeout> | null = null;

	function onSearchInput() {
		if (searchTimer) clearTimeout(searchTimer);
		searchTimer = setTimeout(() => void setRankSearch(searchText), 300);
	}

	function clearSearch() {
		if (searchTimer) clearTimeout(searchTimer);
		searchText = '';
		void setRankSearch('');
	}

	// Open a ranked device on the traffic graph over the same window: select
	// it and search for it so the graph highlights and centers it.
	function openInTraffic(hostname: string, nodeId: string) {
		filterStore.setSearch(trafficSearchFor(hostname, nodeId));
		uiStore.selectNode(nodeId);
		dataSourceStore.handOffWindow();
		void goto('/');
	}

	onDestroy(() => {
		if (searchTimer) clearTimeout(searchTimer);
	});

	function sortArrow(active: boolean): string {
		return active ? ' \u25BE' : '';
	}
</script>

{#snippet nodeName(hostname: string, nodeId: string)}
	{@const label = rankNodeLabel(hostname, nodeId)}
	<button
		type="button"
		class="max-w-full truncate text-left hover:underline {label.mono ? 'font-mono text-xs text-muted-foreground' : 'font-medium'}"
		title={`${nodeId}: open on the traffic graph`}
		onclick={() => openInTraffic(hostname, nodeId)}
	>
		{label.text}
	</button>
{/snippet}

{#snippet ownerLine(owner: string | undefined, hostname = '', nodeId = '')}
	{@const hint = subnetRouteHint(hostname, nodeId)}
	{#if owner}
		<div class="truncate text-[10px] text-muted-foreground" title={owner}>{owner}</div>
	{:else if hint}
		<div class="truncate text-[10px] text-muted-foreground/70">{hint}</div>
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

<section class="mb-4 sm:mb-5" aria-label="Top talkers and pairs">
	<div class="mb-3 flex flex-wrap items-center gap-2">
		<div class="flex items-center gap-1 text-xs text-muted-foreground">
			<ArrowUpDown class="h-3.5 w-3.5" />
			<span class="hidden sm:inline">Sort</span>
		</div>
		<div class="flex rounded-md border border-border bg-muted/30 p-0.5" role="group" aria-label="Sort talkers and pairs">
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

		<div class="relative ml-auto w-full min-w-0 sm:w-72">
			<Search class="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
			<input
				type="text"
				role="searchbox"
				class="h-8 w-full rounded-md border border-border bg-background pl-7 pr-7 text-xs placeholder:text-muted-foreground"
				placeholder="Search name, owner email, IP, tag:x"
				aria-label="Search talkers and pairs"
				bind:value={searchText}
				oninput={onSearchInput}
				onkeydown={(event) => event.key === 'Escape' && clearSearch()}
			/>
			{#if searchText}
				<button
					type="button"
					class="absolute right-1.5 top-1/2 -translate-y-1/2 rounded p-0.5 text-muted-foreground hover:text-foreground"
					aria-label="Clear search"
					onclick={clearSearch}
				>
					<X class="h-3.5 w-3.5" />
				</button>
			{/if}
		</div>
	</div>

	<div class="grid grid-cols-1 items-start gap-3 lg:grid-cols-2">
		<section class="rounded-lg border border-border bg-card p-3 sm:p-4" aria-labelledby="ranked-talkers-heading">
			<div class="mb-3 flex flex-wrap items-center justify-between gap-2">
				<div class="flex items-center gap-2">
					<h3 id="ranked-talkers-heading" class="text-sm font-medium text-muted-foreground">Top Talkers</h3>
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
					<p class="text-sm text-muted-foreground">
						{$rankSearch ? `No devices match "${$rankSearch}" in this window` : 'No device traffic in this window'}
					</p>
				</div>
			{:else}
				<div class="hidden overflow-x-auto sm:block">
					<table class="w-full text-sm">
						<thead>
							<tr class="border-b border-border text-left text-muted-foreground">
								<th class="pb-2 pr-4">#</th>
								<th class="pb-2 pr-4">Device</th>
								<th class="pb-2 pr-4">Owner</th>
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
										{@render ownerLine(undefined, talker.hostname, talker.nodeId)}
									</td>
									<td class="max-w-[180px] truncate py-1.5 pr-4 text-xs text-muted-foreground" title={talker.owner}>
										{talker.owner ?? ''}
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
									<div class="min-w-0">
										{@render nodeName(talker.hostname, talker.nodeId)}
										{@render ownerLine(talker.owner, talker.hostname, talker.nodeId)}
									</div>
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
					<h3 id="ranked-pairs-heading" class="text-sm font-medium text-muted-foreground">Top Pairs</h3>
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
					<p class="text-sm text-muted-foreground">
						{$rankSearch ? `No pairs match "${$rankSearch}" in this window` : 'No communication pairs in this window'}
					</p>
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
										{@render ownerLine(pair.srcOwner, pair.srcHostname, pair.srcNodeId)}
									</td>
									<td class="max-w-[160px] truncate py-1.5 pr-4">
										{@render nodeName(pair.dstHostname, pair.dstNodeId)}
										{@render ownerLine(pair.dstOwner, pair.dstHostname, pair.dstNodeId)}
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
</section>
