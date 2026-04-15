<script lang="ts">
	import { Clock, Database, Radio } from 'lucide-svelte';
	import { dataSourceStore, hasHistoricalData } from '$lib/stores/data-source-store';
	import { loadNetworkData, startAutoRefresh, stopAutoRefresh, AUTO_REFRESH_INTERVAL } from '$lib/stores';
	import { onMount } from 'svelte';

	// Debounce timer for reloading data
	let reloadTimeout: ReturnType<typeof setTimeout> | null = null;

	function scheduleReload() {
		if (reloadTimeout) clearTimeout(reloadTimeout);
		reloadTimeout = setTimeout(() => {
			loadNetworkData();
		}, 300);
	}

	// Format date for display
	function formatDate(date: Date | string | null): string {
		if (!date) return '--';
		const d = typeof date === 'string' ? new Date(date) : date;
		return d.toLocaleString(undefined, {
			month: 'short',
			day: 'numeric',
			hour: '2-digit',
			minute: '2-digit'
		});
	}

	function formatTime(date: Date | null): string {
		if (!date) return '--:--';
		return date.toLocaleTimeString(undefined, {
			hour: '2-digit',
			minute: '2-digit',
			second: '2-digit'
		});
	}

	// Range slider values (0-100)
	let startValue = $state(0);
	let endValue = $state(100);

	// Track which handle is being dragged
	let dragging: 'start' | 'end' | null = $state(null);
	// DOM ref - doesn't need $state as it's only used imperatively for position calculations
	let sliderTrack: HTMLDivElement = undefined!;

	// Fetch data range on mount
	onMount(() => {
		dataSourceStore.fetchDataRange();
		dataSourceStore.fetchPollerStatus();

		// Refresh poller status periodically
		const interval = setInterval(() => {
			dataSourceStore.fetchPollerStatus();
		}, 30000);

		return () => {
			clearInterval(interval);
			if (reloadTimeout) clearTimeout(reloadTimeout);
			window.removeEventListener('mousemove', handleMouseMove);
			window.removeEventListener('mouseup', handleMouseUp);
			window.removeEventListener('touchmove', handleMouseMove);
			window.removeEventListener('touchend', handleMouseUp);
		};
	});

	// Convert slider value to time
	function valueToTime(value: number): Date | null {
		const range = $dataSourceStore.dataRange;
		if (!range || !range.earliest || !range.latest) return null;
		const earliest = new Date(range.earliest).getTime();
		const latest = new Date(range.latest).getTime();
		return new Date(earliest + (latest - earliest) * (value / 100));
	}

	// Update store when slider changes
	function updateSelectedRange() {
		const start = valueToTime(startValue);
		const end = valueToTime(endValue);
		if (start && end) {
			dataSourceStore.setSelectedRange(start, end);
			scheduleReload();
		}
	}

	// Handle mouse/touch events for custom range slider
	function handleMouseDown(handle: 'start' | 'end') {
		return (e: MouseEvent | TouchEvent) => {
			e.preventDefault();
			dragging = handle;
			window.addEventListener('mousemove', handleMouseMove);
			window.addEventListener('mouseup', handleMouseUp);
			window.addEventListener('touchmove', handleMouseMove);
			window.addEventListener('touchend', handleMouseUp);
		};
	}

	function handleMouseMove(e: MouseEvent | TouchEvent) {
		if (!dragging || !sliderTrack) return;

		const rect = sliderTrack.getBoundingClientRect();
		// Guard against empty touches array (shouldn't happen for touchmove, but be safe)
		const clientX = 'touches' in e && e.touches.length > 0 ? e.touches[0].clientX : (e as MouseEvent).clientX;
		let value = ((clientX - rect.left) / rect.width) * 100;
		value = Math.max(0, Math.min(100, value));

		if (dragging === 'start') {
			startValue = Math.min(value, endValue - 2); // Keep minimum gap
		} else {
			endValue = Math.max(value, startValue + 2); // Keep minimum gap
		}

		updateSelectedRange();
	}

	function handleMouseUp() {
		dragging = null;
		window.removeEventListener('mousemove', handleMouseMove);
		window.removeEventListener('mouseup', handleMouseUp);
		window.removeEventListener('touchmove', handleMouseMove);
		window.removeEventListener('touchend', handleMouseUp);
	}

	// Check if the data range has valid (non-zero) timestamps
	function hasValidDataRange(): boolean {
		const range = $dataSourceStore.dataRange;
		if (!range || !range.earliest || !range.latest || range.count === 0) return false;
		return new Date(range.earliest).getFullYear() > 1970;
	}

	function toggleMode() {
		const newMode = $dataSourceStore.mode === 'live' ? 'historical' : 'live';

		if (newMode === 'historical' && !hasValidDataRange()) {
			// No stored data yet - don't switch
			return;
		}

		dataSourceStore.setMode(newMode);

		if (newMode === 'historical') {
			// Stop auto-refresh - historical data is static
			stopAutoRefresh();
			// Set initial range to last 10% of available data
			startValue = 90;
			endValue = 100;
			const start = valueToTime(startValue);
			const end = valueToTime(endValue);
			if (start && end) {
				dataSourceStore.setSelectedRange(start, end);
			}
		} else {
			// Restart auto-refresh when switching back to live mode
			startAutoRefresh(AUTO_REFRESH_INTERVAL);
		}
		// Single load - cancel any pending debounced reload
		if (reloadTimeout) {
			clearTimeout(reloadTimeout);
			reloadTimeout = null;
		}
		loadNetworkData();
	}

	// Computed values
	const dataRange = $derived($dataSourceStore.dataRange);
	const pollerStatus = $derived($dataSourceStore.pollerStatus);
	const mode = $derived($dataSourceStore.mode);
	const selectedStart = $derived($dataSourceStore.selectedStart);
	const selectedEnd = $derived($dataSourceStore.selectedEnd);
	const canSwitchToHistorical = $derived(mode === 'historical' || hasValidDataRange());
</script>

<div class="space-y-3">
	<!-- Mode Toggle -->
	<div class="flex items-center justify-between">
		<span class="text-sm font-medium">Data Source</span>
		<button
			onclick={toggleMode}
			disabled={mode === 'live' && !canSwitchToHistorical}
			class="flex items-center gap-2 rounded-md px-3 py-1.5 text-sm transition-colors
				{mode === 'live'
				? 'bg-green-500/20 text-green-400 hover:bg-green-500/30'
				: 'bg-blue-500/20 text-blue-400 hover:bg-blue-500/30'}
				disabled:opacity-40 disabled:cursor-not-allowed"
			title={!canSwitchToHistorical && mode === 'live' ? 'No stored data yet - wait for poller to collect data' : ''}
		>
			{#if mode === 'live'}
				<Radio class="h-4 w-4" />
				Live
			{:else}
				<Database class="h-4 w-4" />
				Historical
			{/if}
		</button>
	</div>

	<!-- Historical Controls (only show in historical mode) -->
	{#if mode === 'historical'}
		<div class="space-y-2 rounded-md border border-border bg-muted/30 p-3">
			<!-- Time Range Display -->
			<div class="flex items-center justify-between text-xs">
				<div class="flex flex-col">
					<span class="text-muted-foreground">From:</span>
					<span class="font-mono font-medium">{formatTime(selectedStart)}</span>
				</div>
				<div class="px-2 text-muted-foreground">→</div>
				<div class="flex flex-col text-right">
					<span class="text-muted-foreground">To:</span>
					<span class="font-mono font-medium">{formatTime(selectedEnd)}</span>
				</div>
			</div>

			<!-- Custom Dual Range Slider -->
			<div class="relative py-2">
				<div
					bind:this={sliderTrack}
					class="relative h-2 rounded-full bg-muted cursor-pointer"
				>
					<!-- Selected range highlight -->
					<div
						class="absolute h-full rounded-full bg-primary/40"
						style="left: {startValue}%; width: {endValue - startValue}%"
					></div>

					<!-- Start handle -->
					<div
						class="absolute top-1/2 -translate-y-1/2 -translate-x-1/2 h-4 w-4 rounded-full bg-primary border-2 border-background cursor-grab shadow-md
							{dragging === 'start' ? 'cursor-grabbing scale-110' : 'hover:scale-110'}"
						style="left: {startValue}%"
						onmousedown={handleMouseDown('start')}
						ontouchstart={handleMouseDown('start')}
						role="slider"
						aria-label="Start time"
						aria-valuemin={0}
						aria-valuemax={100}
						aria-valuenow={startValue}
						tabindex="0"
					></div>

					<!-- End handle -->
					<div
						class="absolute top-1/2 -translate-y-1/2 -translate-x-1/2 h-4 w-4 rounded-full bg-primary border-2 border-background cursor-grab shadow-md
							{dragging === 'end' ? 'cursor-grabbing scale-110' : 'hover:scale-110'}"
						style="left: {endValue}%"
						onmousedown={handleMouseDown('end')}
						ontouchstart={handleMouseDown('end')}
						role="slider"
						aria-label="End time"
						aria-valuemin={0}
						aria-valuemax={100}
						aria-valuenow={endValue}
						tabindex="0"
					></div>
				</div>
			</div>

			<!-- Range Labels -->
			{#if dataRange}
				<div class="flex justify-between text-xs text-muted-foreground">
					<span>{formatDate(dataRange.earliest)}</span>
					<span>{formatDate(dataRange.latest)}</span>
				</div>
			{/if}
		</div>
	{/if}

	<!-- Data Stats -->
	{#if pollerStatus}
		<div class="space-y-1 text-xs text-muted-foreground">
			<div class="flex justify-between">
				<span>Stored Records:</span>
				<span class="font-mono">{(pollerStatus.database?.dataRange?.count ?? pollerStatus.database?.tableCounts?.flow_logs_current ?? 0).toLocaleString()}</span>
			</div>
			{#if pollerStatus.lastPollTime && new Date(pollerStatus.lastPollTime).getFullYear() > 1970}
				<div class="flex justify-between">
					<span>Last Poll:</span>
					<span class="font-mono">{formatDate(pollerStatus.lastPollTime)}</span>
				</div>
			{:else if pollerStatus.pollErrors > 0}
				<div class="flex justify-between">
					<span>Last Poll:</span>
					<span class="font-mono text-destructive">Failed ({pollerStatus.pollErrors} errors)</span>
				</div>
			{/if}
			<div class="flex justify-between">
				<span>Poll Interval:</span>
				<span class="font-mono">{pollerStatus.pollInterval}</span>
			</div>
		</div>
	{:else if !$hasHistoricalData}
		<div class="text-xs text-muted-foreground">
			<Clock class="mb-1 inline h-3 w-3" />
			Collecting data... Historical view will be available soon.
		</div>
	{/if}
</div>
