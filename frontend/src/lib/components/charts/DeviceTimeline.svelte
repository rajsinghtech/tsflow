<script lang="ts">
	import { page } from '$app/state';
	import { Loader2 } from 'lucide-svelte';
	import { queryTimeWindow } from '#lib/stores/data-source-store';
	import { filterStore } from '#lib/stores/filter-store';
	import { uiStore } from '#lib/stores/ui-store';
	import { filteredNodes } from '#lib/stores/network-store';
	import { tailscaleService } from '#lib/services';
	import { selectedDeviceId, timelineColumns, type TimelineColumn } from '#lib/analytics/device-timeline';
	import { formatBytes } from '#lib/utils';

	let { nodeId = null }: { nodeId?: string | null } = $props();

	interface PeerRow {
		peerId: string;
		hostname: string;
		totalBytes: number;
		flowCount: number;
	}

	const pageSize = 5;
	let hostname = $state('');
	let columns = $state<TimelineColumn[]>([]);
	let peers = $state<PeerRow[]>([]);
	let hasMore = $state(false);
	let offset = $state(0);
	let loading = $state(false);
	let error = $state<string | null>(null);
	let loadToken = 0;
	let loadedDevice = '';

	const deviceId = $derived(
		nodeId ?? selectedDeviceId(page.url.searchParams.get('device'), $uiStore.selectedNodeId, $filteredNodes)
	);

	const maxTotal = $derived(columns.reduce((max, column) => Math.max(max, column.total), 0));

	function barHeight(value: number): number {
		if (maxTotal <= 0) return 0;
		return Math.max(value > 0 ? 2 : 0, Math.round((value / maxTotal) * 64));
	}

	function timeLabel(value: string): string {
		const parsed = new Date(value);
		if (Number.isNaN(parsed.getTime())) return value;
		return parsed.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
	}

	async function load(nodeId: string, start: Date, end: Date, types: string[], pageOffset: number) {
		const token = ++loadToken;
		loading = true;
		error = null;
		try {
			const response = await tailscaleService.getDeviceTimeline(nodeId, start, end, {
				limit: pageSize,
				offset: pageOffset,
				trafficTypes: types
			});
			if (token !== loadToken) return;
			hostname = response.hostname || nodeId;
			columns = timelineColumns(response.buckets || []);
			peers = response.peers || [];
			hasMore = response.metadata?.hasMore ?? false;
		} catch (err) {
			if (token !== loadToken) return;
			hostname = nodeId;
			columns = [];
			peers = [];
			hasMore = false;
			error = err instanceof Error ? err.message : 'Failed to load device timeline';
		} finally {
			if (token === loadToken) loading = false;
		}
	}

	$effect(() => {
		const nodeId = deviceId;
		if ((nodeId ?? '') !== loadedDevice) {
			loadedDevice = nodeId ?? '';
			offset = 0;
		}
		const pageOffset = offset;
		if (!nodeId) {
			columns = [];
			peers = [];
			return;
		}
		const range = $queryTimeWindow;
		if (!range?.start || !range?.end || range.end <= range.start) return;
		void load(nodeId, range.start, range.end, $filterStore.trafficTypes, pageOffset);
	});
</script>

{#if deviceId}
	<section class="shrink-0 border-t border-border bg-card px-3 py-2 sm:px-4">
		<div class="md:grid md:grid-cols-[minmax(0,2fr)_minmax(0,1fr)] md:gap-6">
			<div class="min-w-0">
				<div class="mb-1 flex items-baseline justify-between gap-2">
					<h2 class="truncate text-sm font-medium">
						<span class="text-primary">{hostname || deviceId}</span>
						<span class="text-muted-foreground"> bytes over time</span>
					</h2>
					{#if loading}
						<Loader2 class="h-3.5 w-3.5 shrink-0 animate-spin text-muted-foreground" />
					{/if}
				</div>
				{#if error}
					<p class="py-4 text-sm text-destructive">{error}</p>
				{:else if columns.length === 0 && !loading}
					<p class="py-4 text-sm text-muted-foreground">No traffic for this device in the selected window.</p>
				{:else}
					<div class="mb-1 flex flex-wrap gap-3 text-[11px] text-muted-foreground">
						<span><span class="mr-1 inline-block h-2 w-2 rounded-sm bg-blue-500"></span>Virtual</span>
						<span><span class="mr-1 inline-block h-2 w-2 rounded-sm bg-green-500"></span>Subnet</span>
						<span><span class="mr-1 inline-block h-2 w-2 rounded-sm bg-purple-500"></span>Exit</span>
						<span><span class="mr-1 inline-block h-2 w-2 rounded-sm bg-amber-500"></span>Physical</span>
					</div>
					<div class="flex h-20 items-end gap-1 overflow-x-auto">
						{#each columns as column}
							<div class="flex h-full min-w-3 flex-1 flex-col justify-end" title="{timeLabel(column.time)} {formatBytes(column.total)}">
								<div class="flex flex-col justify-end" style="height: {barHeight(column.total)}px">
									{#if column.physical > 0}
										<div class="bg-amber-500" style="height: {(column.physical / column.total) * 100}%"></div>
									{/if}
									{#if column.exit > 0}
										<div class="bg-purple-500" style="height: {(column.exit / column.total) * 100}%"></div>
									{/if}
									{#if column.subnet > 0}
										<div class="bg-green-500" style="height: {(column.subnet / column.total) * 100}%"></div>
									{/if}
									{#if column.virtual > 0}
										<div class="bg-blue-500" style="height: {(column.virtual / column.total) * 100}%"></div>
									{/if}
								</div>
							</div>
						{/each}
					</div>
				{/if}
			</div>

			<div class="mt-2 min-w-0 md:mt-0">
				<div class="mb-1 flex items-center justify-between gap-2">
					<h3 class="text-xs font-medium text-muted-foreground">Top peers</h3>
					{#if offset > 0 || hasMore}
						<div class="flex gap-1 text-xs">
							<button
								class="rounded-md border border-border px-2 py-0.5 hover:bg-secondary disabled:opacity-40"
								disabled={offset === 0}
								onclick={() => {
									offset = Math.max(0, offset - pageSize);
								}}
							>
								Previous
							</button>
							<button
								class="rounded-md border border-border px-2 py-0.5 hover:bg-secondary disabled:opacity-40"
								disabled={!hasMore}
								onclick={() => {
									offset += pageSize;
								}}
							>
								Next
							</button>
						</div>
					{/if}
				</div>
				{#if peers.length === 0 && !loading}
					<p class="text-xs text-muted-foreground">No peers in this window.</p>
				{:else}
					<ul class="divide-y divide-border/60 text-xs">
						{#each peers as peer}
							<li class="flex items-center justify-between gap-3 py-0.5">
								<span class="truncate">{peer.hostname || peer.peerId}</span>
								<span class="shrink-0 tabular-nums text-muted-foreground">{formatBytes(peer.totalBytes)}</span>
							</li>
						{/each}
					</ul>
				{/if}
			</div>
		</div>
	</section>
{/if}
