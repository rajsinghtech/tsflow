<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { Loader2, AlertCircle, RefreshCw, X, Keyboard } from 'lucide-svelte';
	import NetworkGraph from '$lib/components/graph/NetworkGraph.svelte';
	import FilterPanel from '$lib/components/filters/FilterPanel.svelte';
	import LogViewer from '$lib/components/logs/LogViewer.svelte';
	import PortDetails from '$lib/components/logs/PortDetails.svelte';
	import BandwidthChart from '$lib/components/charts/BandwidthChart.svelte';
	import EdgePolicyInfo from '$lib/components/logs/EdgePolicyInfo.svelte';
	import Header from '$lib/components/layout/Header.svelte';
	import { loadNetworkData, retryLoadNetworkData, retryCount, retryingIn, startAutoRefresh, stopAutoRefresh, filteredNodes, filteredEdges } from '$lib/stores/network-store';
	import { uiStore } from '$lib/stores/ui-store';

	onMount(() => {
		loadNetworkData();
		startAutoRefresh(60_000);
	});

	onDestroy(() => {
		stopAutoRefresh();
	});

	// Keyboard shortcuts
	let showShortcuts = $state(false);

	function handleKeydown(e: KeyboardEvent) {
		// Ignore when typing in inputs
		if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return;

		if (e.key === 'Escape') {
			if (showShortcuts) {
				showShortcuts = false;
			} else {
				uiStore.clearSelection();
			}
		} else if (e.key === '?' && !e.metaKey && !e.ctrlKey) {
			showShortcuts = !showShortcuts;
		} else if (e.key === 'r' && !e.metaKey && !e.ctrlKey) {
			loadNetworkData();
		} else if (e.key === 'f' && !e.metaKey && !e.ctrlKey) {
			uiStore.toggleFilters();
		} else if (e.key === 'l' && !e.metaKey && !e.ctrlKey) {
			uiStore.toggleLogViewer();
		}
	}

	const shortcuts = [
		{ key: 'R', desc: 'Refresh data' },
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

<div class="flex h-screen flex-col bg-background">
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
			<aside class="fixed inset-y-0 left-0 z-50 flex w-72 max-w-[90vw] flex-col overflow-y-auto bg-card shadow-2xl lg:hidden">
				<div class="flex items-center justify-between border-b border-border px-4 py-3">
					<h2 class="text-sm font-semibold">Filters</h2>
					<button
						onclick={() => uiStore.closeMobileDrawer()}
						class="rounded-md p-2 hover:bg-secondary"
					>
						<X class="h-5 w-5" />
					</button>
				</div>
				<FilterPanel />
			</aside>
		{/if}

		<!-- Graph Area -->
		<main id="main-content" class="relative flex flex-1 flex-col overflow-hidden">
			<!-- Loading State -->
			{#if $uiStore.isLoading && $filteredNodes.length === 0}
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
			<!-- Graph -->
			{:else}
				<div class="flex-1" style="height: calc(100% - {$uiStore.showLogViewer ? logViewerHeight + 110 : 0}px)">
					<NetworkGraph
						nodes={$filteredNodes}
						edges={$filteredEdges}
					/>
				</div>
			{/if}

			<!-- Bottom Panel: Bandwidth Chart + Port Details + Log Viewer -->
			{#if $uiStore.showLogViewer}
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
				<div class="overflow-hidden border-t border-border bg-card" style="height: {logViewerHeight}px">
					<LogViewer />
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
