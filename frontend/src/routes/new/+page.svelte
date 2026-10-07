<script lang="ts">
	import { onMount } from 'svelte';
	import { Loader2, RefreshCw, Waypoints } from 'lucide-svelte';
	import Header from '#lib/components/layout/Header.svelte';
	import TimelineSlider from '#lib/components/timeline/TimelineSlider.svelte';
	import { dataSourceStore, filterStore, queryTimeWindow } from '#lib/stores';
	import { tailscaleService } from '#lib/services';
	import { DEFAULT_NEW_PAIR_LOOKBACK, NEW_PAIR_LOOKBACKS, lookbackNotice, type NewPairCoverage } from '#lib/analytics/new-pairs';
	import { formatBytes } from '#lib/utils';
	import type { TrafficType } from '#lib/types';

	interface NewPairRow {
		srcNodeId: string;
		srcHostname: string;
		dstNodeId: string;
		dstHostname: string;
		txBytes: number;
		rxBytes: number;
		totalBytes: number;
		flowCount: number;
		firstSeen: string;
	}

	const pageSize = 20;
	const trafficTypes: { value: TrafficType; label: string }[] = [
		{ value: 'virtual', label: 'Virtual' },
		{ value: 'subnet', label: 'Subnet' },
		{ value: 'exit', label: 'Exit Node' },
		{ value: 'physical', label: 'Physical' }
	];

	let pairs = $state<NewPairRow[]>([]);
	let hasMore = $state(false);
	let coverage = $state<NewPairCoverage | undefined>(undefined);
	let offset = $state(0);
	let lookback = $state(DEFAULT_NEW_PAIR_LOOKBACK);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let showWindow = $state(false);
	let loadToken = 0;

	const selectedTrafficTypes = $derived(new Set($filterStore.trafficTypes));
	const coverageNotice = $derived(lookbackNotice(coverage, seenDate));

	function seenDate(value: Date): string {
		return value.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
	}

	function endpointLabel(id: string, hostname: string): string {
		return hostname || id;
	}

	function seenLabel(value: string): string {
		const parsed = new Date(value);
		if (Number.isNaN(parsed.getTime())) return value;
		return parsed.toLocaleString(undefined, {
			month: 'short',
			day: 'numeric',
			hour: '2-digit',
			minute: '2-digit'
		});
	}

	function setLookback(value: string) {
		lookback = value;
		offset = 0;
	}

	function toggleTrafficType(type: TrafficType) {
		const next = new Set($filterStore.trafficTypes);
		if (next.has(type)) next.delete(type);
		else next.add(type);
		filterStore.setTrafficTypes([...next]);
		offset = 0;
	}

	async function load(start: Date, end: Date, types: TrafficType[], pageOffset: number, selectedLookback: string) {
		if (types.length === 0) {
			pairs = [];
			hasMore = false;
			coverage = undefined;
			loading = false;
			error = null;
			return;
		}
		const token = ++loadToken;
		loading = true;
		error = null;
		try {
			const response = await tailscaleService.getNewPairs(start, end, {
				lookback: selectedLookback,
				limit: pageSize,
				offset: pageOffset,
				trafficTypes: types
			});
			if (token !== loadToken) return;
			pairs = response.pairs || [];
			hasMore = response.metadata?.hasMore ?? false;
			coverage = response.metadata;
		} catch (err) {
			if (token !== loadToken) return;
			pairs = [];
			hasMore = false;
			coverage = undefined;
			error = err instanceof Error ? err.message : 'Failed to load new connections';
		} finally {
			if (token === loadToken) loading = false;
		}
	}

	onMount(() => {
		let cancelled = false;
		void (async () => {
			const range = await dataSourceStore.fetchDataRange();
			if (cancelled) return;
			if (range?.count) dataSourceStore.showLatestWindow(range);
		})();
		return () => {
			cancelled = true;
		};
	});

	$effect(() => {
		const range = $queryTimeWindow;
		const types = $filterStore.trafficTypes;
		const pageOffset = offset;
		const selectedLookback = lookback;
		if (!range?.start || !range?.end || range.end <= range.start) return;
		void load(range.start, range.end, types, pageOffset, selectedLookback);
	});
</script>

<div class="flex h-screen flex-col bg-background">
	<Header />

	<main class="flex-1 overflow-y-auto p-3 sm:p-6">
		<div class="mb-4 flex flex-wrap items-center justify-between gap-2">
			<div>
				<h2 class="text-base font-semibold">New connections</h2>
				<p class="text-xs text-muted-foreground">
					Pairs first seen in this window, and not seen in the lookback before it.
				</p>
			</div>
			<button
				class="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs hover:bg-secondary"
				onclick={() => {
					const range = $queryTimeWindow;
					void load(range.start, range.end, $filterStore.trafficTypes, offset, lookback);
				}}
			>
				<RefreshCw class="h-3.5 w-3.5" />
				Refresh
			</button>
		</div>

		<div class="mb-4 flex flex-wrap items-center gap-2 rounded-lg border border-border bg-card p-2">
			<button
				type="button"
				class="inline-flex min-h-8 items-center rounded-md border border-border px-2.5 text-xs hover:bg-secondary"
				class:bg-secondary={showWindow}
				onclick={() => (showWindow = !showWindow)}
			>
				Window
			</button>
			<label class="flex items-center gap-2 text-xs text-muted-foreground" for="new-pair-lookback">
				Lookback
				<select
					id="new-pair-lookback"
					class="rounded-md border border-input bg-background px-2 py-1 text-xs text-foreground"
					value={lookback}
					onchange={(e) => setLookback(e.currentTarget.value)}
				>
					{#each NEW_PAIR_LOOKBACKS as option}
						<option value={option.value}>{option.label}</option>
					{/each}
				</select>
			</label>
			<div class="flex flex-wrap gap-1">
				{#each trafficTypes as type}
					<button
						type="button"
						class="inline-flex min-h-7 items-center rounded-md border px-2 text-xs"
						class:border-primary={selectedTrafficTypes.has(type.value)}
						class:bg-secondary={selectedTrafficTypes.has(type.value)}
						class:border-border={!selectedTrafficTypes.has(type.value)}
						onclick={() => toggleTrafficType(type.value)}
					>
						{type.label}
					</button>
				{/each}
			</div>
		</div>

		{#if showWindow}
			<div class="mb-4 rounded-lg border border-border bg-card p-2">
				<TimelineSlider
					onWindowChange={() => {
						offset = 0;
					}}
				/>
			</div>
		{/if}

		{#if coverageNotice && !error}
			<p class="mb-3 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-300" role="status">
				{coverageNotice}
			</p>
		{/if}

		<div class="rounded-lg border border-border bg-card p-3 sm:p-4">
			{#if loading && pairs.length === 0}
				<div class="flex items-center justify-center py-12 text-muted-foreground">
					<Loader2 class="h-5 w-5 animate-spin" />
				</div>
			{:else if error}
				<p class="py-8 text-center text-sm text-destructive">{error}</p>
			{:else if pairs.length === 0}
				<div class="flex flex-col items-center justify-center py-12 text-center">
					<Waypoints class="mb-2 h-8 w-8 text-muted-foreground/30" />
					<p class="text-sm text-muted-foreground">No new connections in this window</p>
					<p class="mt-1 text-xs text-muted-foreground/60">
						A pair already seen during the lookback stays off this list.
					</p>
				</div>
			{:else}
				<div class="overflow-x-auto">
					<table class="w-full text-sm">
						<thead>
							<tr class="border-b border-border text-left text-muted-foreground">
								<th class="pb-2 pr-4">First seen</th>
								<th class="pb-2 pr-4">Source</th>
								<th class="pb-2 pr-4">Destination</th>
								<th class="pb-2 pr-4 text-right">Traffic</th>
								<th class="pb-2 text-right">Flows</th>
							</tr>
						</thead>
						<tbody>
							{#each pairs as pair}
								<tr class="border-b border-border/50">
									<td class="py-1.5 pr-4 text-xs tabular-nums">{seenLabel(pair.firstSeen)}</td>
									<td class="max-w-[180px] truncate py-1.5 pr-4" title={pair.srcNodeId}>
										{endpointLabel(pair.srcNodeId, pair.srcHostname)}
									</td>
									<td class="max-w-[180px] truncate py-1.5 pr-4" title={pair.dstNodeId}>
										{endpointLabel(pair.dstNodeId, pair.dstHostname)}
									</td>
									<td class="py-1.5 pr-4 text-right tabular-nums">{formatBytes(pair.totalBytes)}</td>
									<td class="py-1.5 text-right tabular-nums">{pair.flowCount.toLocaleString()}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
				<div class="mt-3 flex items-center justify-between text-xs text-muted-foreground">
					<span>{offset + 1}–{offset + pairs.length}</span>
					<div class="flex gap-1">
						<button
							class="rounded-md border border-border px-2 py-1 hover:bg-secondary disabled:opacity-40"
							disabled={offset === 0}
							onclick={() => (offset = Math.max(0, offset - pageSize))}
						>
							Previous
						</button>
						<button
							class="rounded-md border border-border px-2 py-1 hover:bg-secondary disabled:opacity-40"
							disabled={!hasMore}
							onclick={() => (offset += pageSize)}
						>
							Next
						</button>
					</div>
				</div>
			{/if}
		</div>
	</main>
</div>
