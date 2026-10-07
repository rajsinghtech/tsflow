<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { meAccess } from '#lib/analytics/landing';
	import { lookbackNotice, type NewPairCoverage } from '#lib/analytics/new-pairs';
	import { viewerReady } from '#lib/stores/viewer-store';
	import { Loader2 } from 'lucide-svelte';
	import Header from '#lib/components/layout/Header.svelte';
	import DeviceTimeline from '#lib/components/charts/DeviceTimeline.svelte';
	import { dataSourceStore, filterStore, queryTimeWindow, viewerStore } from '#lib/stores';
	import { pageRefresh } from '#lib/stores/live-mode';
	import { tailscaleService } from '#lib/services';
	import { formatBytes } from '#lib/utils';

	interface ViewerDevice {
		nodeId: string;
		hostname: string;
		owner: string;
		online: boolean;
		totalBytes: number;
		flowCount: number;
	}

	interface NewPairRow {
		srcNodeId: string;
		srcHostname: string;
		dstNodeId: string;
		dstHostname: string;
		totalBytes: number;
		flowCount: number;
		firstSeen: string;
	}

	let devices = $state<ViewerDevice[]>([]);
	let login = $state('');
	let totalBytes = $state(0);
	let flowCount = $state(0);
	let peers = $state<Array<{ peerId: string; hostname: string; totalBytes: number }>>([]);
	let newPairs = $state<NewPairRow[]>([]);
	let coverage = $state<NewPairCoverage | undefined>(undefined);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let opened = $state<string | null>(null);
	// Wait for the stored range: until then a live window is measured from the
	// wall clock rather than the latest stored data.
	let rangeReady = $state(false);
	let loadToken = 0;
	let lastLoadKey = '';

	const coverageNotice = $derived(
		lookbackNotice(coverage, (d) => d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }))
	);

	function seenLabel(value: string): string {
		const parsed = new Date(value);
		if (Number.isNaN(parsed.getTime())) return value;
		return parsed.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
	}

	async function load(start: Date, end: Date, types: string[]) {
		const token = ++loadToken;
		loading = true;
		error = null;
		try {
			const response = await tailscaleService.getViewerSummary(start, end, { trafficTypes: types, lookback: '7d' });
			if (token !== loadToken) return;
			login = response.login;
			devices = response.devices || [];
			totalBytes = response.traffic?.totalBytes ?? 0;
			flowCount = response.traffic?.flowCount ?? 0;
			peers = response.peers || [];
			newPairs = response.newPairs || [];
			coverage = response.metadata;
		} catch (err) {
			if (token !== loadToken) return;
			devices = [];
			peers = [];
			newPairs = [];
			coverage = undefined;
			error = err instanceof Error ? err.message : 'Failed to load your devices';
		} finally {
			if (token === loadToken) loading = false;
		}
	}

	onMount(() => {
		let cancelled = false;
		void dataSourceStore.fetchDataRange().finally(() => {
			if (!cancelled) rangeReady = true;
		});
		return () => {
			cancelled = true;
		};
	});

	const access = $derived(meAccess($viewerReady, $viewerStore?.login));

	$effect(() => {
		if (access === 'redirect') void goto('/' + page.url.search, { replaceState: true });
	});

	$effect(() => {
		if (access !== 'show') return;
		const range = $queryTimeWindow;
		const types = $filterStore.trafficTypes;
		const refreshCount = $pageRefresh;
		if (!rangeReady) return;
		if (!range?.start || !range?.end || range.end <= range.start) return;
		// The range poll replaces the window with equal dates while live; only a
		// real change or an explicit refresh reloads.
		const loadKey = [range.start.getTime(), range.end.getTime(), types.join(','), refreshCount].join('|');
		if (loadKey === lastLoadKey) return;
		lastLoadKey = loadKey;
		void load(range.start, range.end, types);
	});
</script>

<div class="flex h-screen flex-col bg-background">
	<Header />
	<main class="flex-1 overflow-y-auto p-3 sm:p-6">
		<div class="mb-4 flex flex-wrap items-center justify-between gap-2">
			<div>
				<h2 class="text-base font-semibold">Me</h2>
				<p class="text-xs text-muted-foreground">{login || $viewerStore?.login || 'Your devices'}</p>
			</div>
		</div>

		{#if loading && devices.length === 0}
			<div class="flex justify-center py-16 text-muted-foreground">
				<Loader2 class="h-5 w-5 animate-spin" />
			</div>
		{:else if error}
			<p class="py-8 text-center text-sm text-destructive">{error}</p>
		{:else}
			<div class="mb-4 grid grid-cols-2 gap-2 sm:grid-cols-3">
				<div class="rounded-lg border border-border bg-card p-3">
					<div class="text-xs text-muted-foreground">Your traffic</div>
					<div class="text-lg font-semibold tabular-nums">{formatBytes(totalBytes)}</div>
				</div>
				<div class="rounded-lg border border-border bg-card p-3">
					<div class="text-xs text-muted-foreground">Flows</div>
					<div class="text-lg font-semibold tabular-nums">{flowCount.toLocaleString()}</div>
				</div>
				<div class="rounded-lg border border-border bg-card p-3">
					<div class="text-xs text-muted-foreground">Devices</div>
					<div class="text-lg font-semibold tabular-nums">{devices.length}</div>
				</div>
			</div>

			<div class="mb-4 rounded-lg border border-border bg-card p-3">
				<h3 class="mb-2 text-sm font-medium text-muted-foreground">Devices</h3>
				{#if devices.length === 0}
					<p class="text-sm text-muted-foreground">No devices are recorded for this login.</p>
				{:else}
					<ul class="divide-y divide-border/60 text-sm">
						{#each devices as device}
							<li>
								<button
									class="flex w-full items-center justify-between gap-3 py-2 text-left hover:bg-secondary/40"
									onclick={() => (opened = opened === device.nodeId ? null : device.nodeId)}
								>
									<span class="flex min-w-0 items-center gap-2">
										<span class="h-2 w-2 shrink-0 rounded-full {device.online ? 'bg-green-500' : 'bg-muted-foreground/40'}"></span>
										<span class="truncate">{device.hostname || device.nodeId}</span>
									</span>
									<span class="shrink-0 tabular-nums text-muted-foreground">{formatBytes(device.totalBytes)}</span>
								</button>
								{#if opened === device.nodeId}
									<DeviceTimeline nodeId={device.nodeId} scoped />
								{/if}
							</li>
						{/each}
					</ul>
				{/if}
			</div>

			<div class="mb-4 grid grid-cols-1 gap-3 lg:grid-cols-2">
				<div class="rounded-lg border border-border bg-card p-3">
					<h3 class="mb-2 text-sm font-medium text-muted-foreground">Top peers</h3>
					{#if peers.length === 0}
						<p class="text-sm text-muted-foreground">No peers in this window.</p>
					{:else}
						<ul class="divide-y divide-border/60 text-sm">
							{#each peers as peer}
								<li class="flex items-center justify-between py-1">
									<span class="truncate">{peer.hostname || peer.peerId}</span>
									<span class="tabular-nums text-muted-foreground">{formatBytes(peer.totalBytes)}</span>
								</li>
							{/each}
						</ul>
					{/if}
				</div>
				<div class="rounded-lg border border-border bg-card p-3">
					<h3 class="mb-2 text-sm font-medium text-muted-foreground">New connections</h3>
					{#if coverageNotice}
						<p class="mb-2 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-300" role="status">
							{coverageNotice}
						</p>
					{/if}
					{#if newPairs.length === 0}
						<p class="text-sm text-muted-foreground">No new connections in this window.</p>
					{:else}
						<ul class="divide-y divide-border/60 text-sm">
							{#each newPairs as pair}
								<li class="flex items-center justify-between gap-3 py-1">
									<span class="min-w-0 truncate">
										{pair.srcHostname || pair.srcNodeId} → {pair.dstHostname || pair.dstNodeId}
										<span class="block text-xs text-muted-foreground">{seenLabel(pair.firstSeen)}</span>
									</span>
									<span class="shrink-0 tabular-nums text-muted-foreground">{formatBytes(pair.totalBytes)}</span>
								</li>
							{/each}
						</ul>
					{/if}
				</div>
			</div>
		{/if}
	</main>
</div>
