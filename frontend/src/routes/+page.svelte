<script lang="ts">
	import { onMount } from 'svelte';
	import NetworkView from '$lib/components/NetworkView.svelte';
	import Sidebar from '$lib/components/Sidebar.svelte';
	import LogViewer from '$lib/components/LogViewer.svelte';
	import { devicesStore, networkLogsStore, filtersStore, servicesStore } from '$lib/stores/data';
	import { ChevronRight } from 'lucide-svelte';

	let loading = $state(true);
	let error = $state<string | null>(null);
	let logViewerHeight = $state(0);
	let sidebarVisible = $state(true);

	// Use Svelte 5's automatic store subscription
	let devices = $derived($devicesStore);
	let networkLogs = $derived($networkLogsStore);
	let filters = $derived($filtersStore);

	async function fetchData() {
		try {
			await Promise.all([
				devicesStore.fetchDevices(),
				networkLogsStore.fetchLogs(),
				servicesStore.fetchServices()
			]);
			loading = false;
			error = null;
		} catch (err) {
			error = err instanceof Error ? err.message : 'Failed to load data';
			loading = false;
		}
	}

	onMount(() => {
		fetchData();

		// Refresh data every 30 seconds
		const interval = setInterval(async () => {
			try {
				await fetchData();
			} catch (err) {
				console.error('Auto-refresh failed:', err);
			}
		}, 30000);

		return () => clearInterval(interval);
	});
</script>

<div class="flex-1 flex flex-col overflow-hidden">
	<div class="flex flex-1 overflow-hidden" style="height: calc(100vh - 60px - {logViewerHeight}px);">
		<!-- Collapsible Sidebar -->
		<div
			class="sidebar-container bg-bg-surface overflow-y-auto flex-shrink-0 transition-all duration-300 {sidebarVisible ? 'w-80' : 'w-0'}"
		>
			<div class="{sidebarVisible ? 'block h-full' : 'hidden'}">
				<Sidebar bind:sidebarVisible={sidebarVisible} />
			</div>
		</div>

		<!-- Main Network View -->
		<div class="flex-1 relative overflow-hidden">
			{#if loading}
				<div class="flex items-center justify-center h-full">
					<div class="text-center">
						<div class="animate-spin rounded-full h-12 w-12 mx-auto" style="border: 3px solid var(--color-bg-interactive); border-top-color: var(--color-text-primary);"></div>
						<p class="mt-4 text-sm font-medium" style="color: var(--color-text-muted);">Loading network data...</p>
					</div>
				</div>
			{:else if error}
				<div class="flex items-center justify-center h-full p-4">
					<div class="card p-8 text-center max-w-md w-full">
						<svg class="w-16 h-16 mx-auto mb-4" style="color: var(--color-text-danger);" fill="none" stroke="currentColor" viewBox="0 0 24 24">
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
						</svg>
						<h3 class="text-lg font-semibold mb-2" style="color: var(--color-text-base);">Connection Error</h3>
						<p class="text-sm mb-4" style="color: var(--color-text-danger);">{error}</p>
						<button
							class="btn btn-primary"
							onclick={() => window.location.reload()}
						>
							Retry Connection
						</button>
					</div>
				</div>
			{:else}
				<NetworkView />
			{/if}

			<!-- Sidebar Toggle Button - Only show when sidebar is closed -->
			{#if !sidebarVisible}
				<button
					onclick={() => sidebarVisible = true}
					class="absolute left-4 top-4 z-10 p-2 rounded-lg transition-colors"
					style="background: var(--color-bg-surface); border: 1px solid var(--color-border-base); box-shadow: var(--shadow-soft); color: var(--color-text-base);"
					onmouseover={(e) => e.currentTarget.style.background = 'var(--color-bg-interactive)'}
					onmouseout={(e) => e.currentTarget.style.background = 'var(--color-bg-surface)'}
					onfocus={(e) => e.currentTarget.style.background = 'var(--color-bg-interactive)'}
					onblur={(e) => e.currentTarget.style.background = 'var(--color-bg-surface)'}
					title="Show filters"
					aria-label="Show filters"
				>
					<ChevronRight class="w-5 h-5" />
				</button>
			{/if}
		</div>
	</div>
</div>

<!-- Log Viewer -->
<LogViewer
	{networkLogs}
	{devices}
	searchQuery={filters.searchQuery}
	trafficTypeFilters={new Set(filters.trafficTypes)}
	onHeightChange={(height) => logViewerHeight = height}
/>

<style>
	/* Hide scrollbar for sidebar while maintaining scroll functionality */
	.sidebar-container {
		scrollbar-width: none; /* Firefox */
		-ms-overflow-style: none; /* IE and Edge */
	}

	.sidebar-container::-webkit-scrollbar {
		display: none; /* Chrome, Safari, Opera */
	}
</style>
