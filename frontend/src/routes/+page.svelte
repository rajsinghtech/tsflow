<script lang="ts">
	import { onMount } from 'svelte';
	import { get } from 'svelte/store';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Loader2, AlertCircle, RefreshCw, X, Keyboard, ChevronUp } from 'lucide-svelte';
	import NetworkGraph from '#lib/components/graph/NetworkGraph.svelte';
	import FilterPanel from '#lib/components/filters/FilterPanel.svelte';
	import LogViewer from '#lib/components/logs/LogViewer.svelte';
	import PortDetails from '#lib/components/logs/PortDetails.svelte';
	import BandwidthChart from '#lib/components/charts/BandwidthChart.svelte';
	import DeviceTimeline from '#lib/components/charts/DeviceTimeline.svelte';
	import EdgePolicyInfo from '#lib/components/logs/EdgePolicyInfo.svelte';
	import Header from '#lib/components/layout/Header.svelte';
	import EmptyRange from '#lib/components/timeline/EmptyRange.svelte';
	import { loadNetworkData, retryLoadNetworkData, retryCount, retryingIn, startAutoRefresh, stopAutoRefresh, filteredNodes, filteredEdges, networkLogs } from '#lib/stores/network-store';
	import { uiStore } from '#lib/stores/ui-store';
	import { dataSourceStore } from '#lib/stores/data-source-store';
	import { viewerReady, viewerStore, whenViewerReady } from '#lib/stores/viewer-store';
	import { landingTarget, readLastTab, rememberTab } from '#lib/analytics/landing';
	import {
		COMPACT_QUERY,
		detailsSheet,
		isCompactViewport,
		sheetDragHeight,
		sheetDragResult
	} from '#lib/stores/details-sheet';

	let isBootstrapping = $state(true);

	$effect(() => {
		if (!$viewerReady) return;
		const target = landingTarget({
			login: $viewerStore?.login,
			pathname: page.url.pathname,
			search: page.url.search,
			stored: readLastTab()
		});
		if (!target || target === page.url.pathname) return;
		rememberTab(target);
		void goto(target);
	});

	onMount(() => {
		let cancelled = false;

		async function bootstrap() {
			isBootstrapping = true;
			await Promise.all([
				dataSourceStore.fetchDataRange(),
				dataSourceStore.fetchPollerStatus(),
				whenViewerReady()
			]);
			if (cancelled) return;
			// A signed-in viewer landing on / is sent to Me. Do not load the graph first.
			const redirect = landingTarget({
				login: get(viewerStore)?.login,
				pathname: page.url.pathname,
				search: page.url.search,
				stored: readLastTab()
			});
			if (redirect && redirect !== page.url.pathname) return;

			startAutoRefresh();

			await loadNetworkData();
			if (!cancelled) {
				isBootstrapping = false;
			}
		}

		bootstrap();

		return () => {
			cancelled = true;
			isBootstrapping = false;
			stopAutoRefresh();
		};
	});

	// Phones and tablets below lg: the details live in a bottom sheet over the graph.
	let compact = $state(isCompactViewport());

	$effect(() => {
		if (typeof window.matchMedia !== 'function') return;
		const query = window.matchMedia(COMPACT_QUERY);
		const update = () => (compact = query.matches);
		update();
		query.addEventListener('change', update);
		return () => query.removeEventListener('change', update);
	});

	// Tapping a node or a connection brings its details up.
	let lastSelection: string | null = null;
	$effect(() => {
		const selection = $uiStore.selectedNodeId ?? $uiStore.selectedEdgeId;
		if (compact && selection && selection !== lastSelection) detailsSheet.reveal();
		lastSelection = selection;
	});

	const sheetTitle = $derived(
		$uiStore.selectedNodeId ? 'Device details' : $uiStore.selectedEdgeId ? 'Connection details' : 'Traffic and logs'
	);

	let mainEl = $state<HTMLElement | null>(null);
	let sheetHeight = $state<number | null>(null);
	let sheetDrag: { startY: number; startHeight: number; pointerId: number } | null = null;
	let dragHeight = $state<number | null>(null);

	function sheetMax(): number {
		return Math.max(0, (mainEl?.clientHeight ?? window.innerHeight) - 48);
	}

	const shownSheetHeight = $derived(
		dragHeight ?? sheetHeight ?? Math.round((mainEl?.clientHeight ?? 600) * 0.55)
	);

	function startSheetDrag(e: PointerEvent) {
		if ((e.target as Element).closest('button')) return;
		sheetDrag = { startY: e.clientY, startHeight: shownSheetHeight, pointerId: e.pointerId };
		(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
	}

	function moveSheetDrag(e: PointerEvent) {
		if (!sheetDrag || e.pointerId !== sheetDrag.pointerId) return;
		dragHeight = sheetDragHeight(sheetDrag.startHeight, e.clientY - sheetDrag.startY, sheetMax());
	}

	function endSheetDrag(e: PointerEvent) {
		if (!sheetDrag || e.pointerId !== sheetDrag.pointerId) return;
		const result = sheetDragResult(sheetDrag.startHeight, e.clientY - sheetDrag.startY, sheetMax());
		sheetDrag = null;
		dragHeight = null;
		sheetHeight = result.height;
		if (!result.open) detailsSheet.close();
	}

	// A tap or a swipe up on the closed handle opens the sheet. This doesn't
	// wait for the click, which Chrome sometimes drops right after a swipe.
	let pillStartY: number | null = null;
	function pillPointerUp(e: PointerEvent) {
		if (pillStartY !== null) {
			const dy = pillStartY - e.clientY;
			if (dy > 30 || Math.abs(dy) < 10) detailsSheet.open();
		}
		pillStartY = null;
	}

	// A tap on the empty graph closes the sheet. Pans and pinches don't.
	let paneTap: { x: number; y: number } | null = null;
	function graphPointerDown(e: PointerEvent) {
		paneTap = (e.target as Element).closest('.svelte-flow__pane') ? { x: e.clientX, y: e.clientY } : null;
	}
	function graphPointerUp(e: PointerEvent) {
		if (paneTap && Math.hypot(e.clientX - paneTap.x, e.clientY - paneTap.y) < 8 && $detailsSheet) {
			detailsSheet.close();
		}
		paneTap = null;
	}

	// Swipe the filter drawer to the left to close it. Touch events, because
	// the browser cancels the pointer once the drawer starts to scroll.
	let drawerStart: { x: number; y: number } | null = null;
	function drawerTouchStart(e: TouchEvent) {
		const touch = e.touches[0];
		drawerStart = e.touches.length === 1 ? { x: touch.clientX, y: touch.clientY } : null;
	}
	function drawerTouchEnd(e: TouchEvent) {
		const touch = e.changedTouches[0];
		if (drawerStart && touch) {
			const dx = drawerStart.x - touch.clientX;
			if (dx > 60 && dx > Math.abs(touch.clientY - drawerStart.y)) uiStore.closeMobileDrawer();
		}
		drawerStart = null;
	}

	// Keyboard shortcuts
	let showShortcuts = $state(false);

	function handleKeydown(e: KeyboardEvent) {
		// Ignore when typing in inputs
		if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return;

		if (e.key === 'Escape') {
			if (showShortcuts) {
				showShortcuts = false;
			} else if (compact && $uiStore.mobileDrawerOpen) {
				uiStore.closeMobileDrawer();
			} else if (compact && $detailsSheet) {
				detailsSheet.close();
			} else {
				uiStore.clearSelection();
			}
		} else if (e.key === '?' && !e.metaKey && !e.ctrlKey) {
			showShortcuts = !showShortcuts;
		} else if (e.key === 'f' && !e.metaKey && !e.ctrlKey) {
			uiStore.toggleFilters();
		} else if (e.key === 'l' && !e.metaKey && !e.ctrlKey) {
			uiStore.toggleLogViewer();
		}
	}

	const shortcuts = [
		{ key: 'R', desc: 'Refresh data' },
		{ key: 'P', desc: 'Pin the window or return to live' },
		{ key: 't ←/→', desc: 'Shift the time range' },
		{ key: 't − / t z', desc: 'Zoom the range out' },
		{ key: 't +', desc: 'Zoom the range in' },
		{ key: 't c', desc: 'Copy a link to this range' },
		{ key: 'F', desc: 'Toggle filters' },
		{ key: 'L', desc: 'Toggle log viewer' },
		{ key: 'Esc', desc: 'Clear selection' },
		{ key: '?', desc: 'Show shortcuts' }
	];

	// Log viewer height state
	let logViewerHeight = $state(300);
	let isResizing = $state(false);

	function handlePointerDown(e: PointerEvent) {
		isResizing = true;
		(e.target as HTMLElement).setPointerCapture(e.pointerId);
	}

	function handlePointerMove(e: PointerEvent) {
		if (!isResizing) return;
		const container = document.getElementById('main-content');
		if (container) {
			const rect = container.getBoundingClientRect();
			logViewerHeight = Math.max(100, Math.min(500, rect.bottom - e.clientY));
		}
	}

	function handlePointerUp() {
		isResizing = false;
	}
</script>

<svelte:window on:pointermove={handlePointerMove} on:pointerup={handlePointerUp} on:keydown={handleKeydown} />

<div class="flex h-dvh flex-col bg-background">
	<!-- Top Bar -->
	<Header />

	<!-- Main Content Area -->
	<div class="relative flex flex-1 overflow-hidden">
		<!-- Desktop Filter Sidebar (lg+) -->
		{#if $uiStore.showFilterPanel}
			<aside class="hidden w-64 shrink-0 overflow-y-auto border-r border-border bg-card lg:block">
				<FilterPanel />
			</aside>
		{/if}

		<!-- Mobile Filter Drawer (< lg) -->
		{#if $uiStore.mobileDrawerOpen}
			<div
				role="button"
				tabindex="0"
				class="fixed inset-0 z-40 bg-black/50 lg:hidden"
				onclick={() => uiStore.closeMobileDrawer()}
				onkeydown={(e) => e.key === 'Escape' && uiStore.closeMobileDrawer()}
				aria-label="Close drawer"
			></div>
			<aside
				class="fixed inset-y-0 left-0 z-50 flex w-72 max-w-[90vw] flex-col overflow-y-auto bg-card shadow-2xl lg:hidden"
				aria-label="Filters"
				ontouchstart={drawerTouchStart}
				ontouchend={drawerTouchEnd}
				ontouchcancel={() => (drawerStart = null)}
			>
				<FilterPanel />
			</aside>
		{/if}

		<!-- Graph Area -->
		<main id="main-content" class="relative flex flex-1 flex-col overflow-hidden" bind:this={mainEl}>
			<!-- Loading State -->
			{#if isBootstrapping || ($uiStore.isLoading && $filteredNodes.length === 0)}
				<div class="flex flex-1 flex-col items-center justify-center gap-4">
					<Loader2 class="h-8 w-8 animate-spin text-primary" />
					<p class="text-muted-foreground">Loading network data...</p>
				</div>
			<!-- Error State -->
			{:else if $uiStore.error}
				<div class="flex flex-1 flex-col items-center justify-center gap-4 p-4">
					<AlertCircle class="h-8 w-8 text-destructive" />
					<div class="text-center">
						<p class="font-medium text-destructive">Failed to load network data</p>
						<p class="mt-1 text-sm text-muted-foreground">{$uiStore.error}</p>
						{#if $retryCount > 0}
							<p class="mt-1 text-xs text-muted-foreground">Attempt {$retryCount} of 3</p>
						{/if}
					</div>
					{#if $retryingIn}
						<div class="mt-2 flex items-center gap-2 text-sm text-muted-foreground">
							<Loader2 class="h-4 w-4 animate-spin" />
							Retrying in {$retryingIn}s...
						</div>
					{:else}
						<button
							onclick={() => retryLoadNetworkData()}
							class="mt-2 flex items-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
						>
							<RefreshCw class="h-4 w-4" />
							Retry
						</button>
					{/if}
				</div>
			<!-- Empty window: nothing stored for this range at all, before any filter -->
			{:else if $networkLogs.length === 0}
				<EmptyRange />
			<!-- Graph -->
			{:else if compact}
				<!-- svelte-ignore a11y_no_static_element_interactions -->
				<div
					class="min-h-0 flex-1"
					onpointerdown={graphPointerDown}
					onpointerup={graphPointerUp}
					onpointercancel={() => (paneTap = null)}
				>
					<NetworkGraph
						nodes={$filteredNodes}
						edges={$filteredEdges}
					/>
				</div>
			{:else}
				<div class="min-h-[40%] flex-1" style="height: calc(100% - {$uiStore.showLogViewer ? logViewerHeight + 110 : 0}px)">
					<NetworkGraph
						nodes={$filteredNodes}
						edges={$filteredEdges}
					/>
				</div>
			{/if}

			{#if compact}
				{#if !isBootstrapping}
					{#if $detailsSheet}
						<section
							class="absolute inset-x-0 bottom-0 z-20 flex flex-col rounded-t-xl border-t border-border bg-card shadow-[0_-8px_24px_rgba(0,0,0,0.18)]"
							style="height: {shownSheetHeight}px"
							aria-label={sheetTitle}
						>
							<!-- svelte-ignore a11y_no_static_element_interactions -->
							<div
								class="relative flex h-11 shrink-0 cursor-row-resize touch-none items-center justify-between pr-1 pl-4 select-none"
								onpointerdown={startSheetDrag}
								onpointermove={moveSheetDrag}
								onpointerup={endSheetDrag}
								onpointercancel={endSheetDrag}
							>
								<div class="absolute top-1.5 left-1/2 h-1 w-10 -translate-x-1/2 rounded-full bg-muted-foreground/40" aria-hidden="true"></div>
								<h2 class="truncate pt-1 text-sm font-semibold">{sheetTitle}</h2>
								<button
									type="button"
									onclick={() => detailsSheet.close()}
									onpointerup={() => detailsSheet.close()}
									class="flex h-11 w-11 items-center justify-center rounded-md text-muted-foreground hover:bg-secondary hover:text-foreground"
									aria-label="Hide details"
									title="Hide details"
								>
									<X class="h-5 w-5" />
								</button>
							</div>
							<div class="min-h-0 flex-1 overflow-y-auto overscroll-contain">
								<DeviceTimeline />
								<BandwidthChart />
								<PortDetails />
								<EdgePolicyInfo />
								<div class="border-t border-border" style="height: {Math.max(240, shownSheetHeight - 44)}px">
									<LogViewer />
								</div>
							</div>
						</section>
					{:else}
						<button
							type="button"
							class="absolute bottom-3 left-1/2 z-20 flex h-11 -translate-x-1/2 touch-none items-center gap-1.5 rounded-full border border-border bg-card px-4 text-sm font-medium shadow-lg hover:bg-secondary"
							onclick={() => detailsSheet.open()}
							onpointerdown={(e) => (pillStartY = e.clientY)}
							onpointerup={pillPointerUp}
							aria-label="Show {sheetTitle.toLowerCase()}"
							title="Show {sheetTitle.toLowerCase()}"
						>
							<ChevronUp class="h-4 w-4" />
							{sheetTitle}
						</button>
					{/if}
				{/if}
			{:else}
			<!-- The graph keeps at least 40% of the height. When a selection's
			     details don't fit under it, this part scrolls instead. -->
			<div class="flex min-h-0 shrink flex-col overflow-y-auto">
			<DeviceTimeline />

			<!-- Bottom Panel: Bandwidth Chart + Port Details + Log Viewer -->
			{#if $uiStore.showLogViewer && !isBootstrapping}
				<BandwidthChart />
				<PortDetails />
				<EdgePolicyInfo />

				<!-- svelte-ignore a11y_no_static_element_interactions -->
				<!-- Resize Handle - taller touch target on mobile -->
				<div
					class="flex h-3 cursor-row-resize items-center justify-center bg-border/50 hover:bg-primary/50 sm:h-1 sm:bg-border sm:hover:bg-primary"
					onpointerdown={handlePointerDown}
					aria-hidden="true"
				>
					<div class="h-0.5 w-8 rounded-full bg-muted-foreground/50 sm:hidden"></div>
				</div>
				<div class="shrink-0 overflow-hidden border-t border-border bg-card" style="height: {logViewerHeight}px">
					<LogViewer />
				</div>
			{/if}
			</div>
			{/if}
		</main>
	</div>
</div>

<!-- Keyboard Shortcuts Help -->
{#if showShortcuts}
	<div
		role="button"
		tabindex="0"
		class="fixed inset-0 z-[100] flex items-center justify-center bg-black/60"
		onclick={() => (showShortcuts = false)}
		onkeydown={(e) => e.key === 'Escape' && (showShortcuts = false)}
		aria-label="Close shortcuts"
	>
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div
			class="w-80 rounded-lg border border-border bg-card p-5 shadow-2xl"
			onclick={(e) => e.stopPropagation()}
			onkeydown={(e) => e.stopPropagation()}
		>
			<div class="mb-4 flex items-center gap-2">
				<Keyboard class="h-5 w-5 text-primary" />
				<h2 class="text-sm font-semibold">Keyboard Shortcuts</h2>
			</div>
			<div class="space-y-2">
				{#each shortcuts as s}
					<div class="flex items-center justify-between text-sm">
						<span class="text-muted-foreground">{s.desc}</span>
						<kbd class="rounded border border-border bg-secondary px-2 py-0.5 font-mono text-xs">{s.key}</kbd>
					</div>
				{/each}
			</div>
			<p class="mt-4 text-center text-xs text-muted-foreground/60">Press Esc or ? to close</p>
		</div>
	</div>
{/if}
