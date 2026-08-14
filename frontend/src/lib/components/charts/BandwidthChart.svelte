<script lang="ts">
	import { queryTimeWindow } from '$lib/stores/data-source-store';
	import { uiStore, filteredNodes, filterStore } from '$lib/stores';
	import { tailscaleService } from '$lib/services';
	import { formatBytes, formatBitsRate } from '$lib/utils';

	// Chart dimensions
	const height = 80;
	const padding = { top: 8, right: 12, bottom: 20, left: 50 };

	// Reactive width based on container
	let containerWidth = $state(800);
	let container: HTMLDivElement;

	// Chart data from API (normalized to bytes/sec for display)
	let chartData = $state<{ time: Date; txBytes: number; rxBytes: number }[]>([]);
	// Raw totals (un-normalized bytes) for header summary
	let rawTotals = $state<{ tx: number; rx: number }>({ tx: 0, rx: 0 });
	let bucketSeconds = $state(60); // Duration of each bucket, from API metadata
	let isLoading = $state(false);
	let emptyReason = $state('No bandwidth data available');
	let bandwidthController: AbortController | null = null;
	let lastBandwidthKey = '';

	// Get selected node and its device ID for bandwidth queries
	const selectedNode = $derived.by(() => {
		const selectedId = $uiStore.selectedNodeId;
		if (!selectedId) return null;
		return $filteredNodes.find((n) => n.id === selectedId) || null;
	});

	// Get the node identifier for bandwidth API queries
	// Priority: device.id (for Tailscale devices) > node's primary IP (for external/subnet nodes)
	const selectedDeviceId = $derived.by(() => {
		if (!selectedNode) return null;
		// For Tailscale devices, use the numeric device ID
		if (selectedNode.device?.id) return selectedNode.device.id;
		// For external/subnet nodes, use the IP (which is how they're stored in DB)
		if (selectedNode.ip) return selectedNode.ip;
		// Fallback to node.id (might be an IP for external nodes)
		return selectedNode.id;
	});

	// Selected node name for display
	const selectedNodeName = $derived(selectedNode?.displayName || null);

	// Check if traffic type filters are active (not showing all types)
	const ALL_TRAFFIC_TYPES = ['virtual', 'subnet', 'exit', 'physical'] as const;
	const hasActiveTrafficFilter = $derived(
		$filterStore.trafficTypes.length > 0 && $filterStore.trafficTypes.length < ALL_TRAFFIC_TYPES.length
	);
	const chartTrafficTypes = $derived.by(() => {
		if (selectedDeviceId) return undefined;
		return $filterStore.trafficTypes;
	});

	$effect(() => {
		if (container) {
			const observer = new ResizeObserver((entries) => {
				containerWidth = entries[0].contentRect.width;
			});
			observer.observe(container);
			return () => observer.disconnect();
		}
	});

	const queryRange = $derived($queryTimeWindow);

	// Fetch bandwidth data when the selected window, node, or traffic filters change.
	$effect(() => {
		const range = queryRange;
		const deviceId = selectedDeviceId;
		if (!range || !isValidRange(range.start, range.end)) {
			clearBandwidth('Select a valid time range');
			return;
		}
		const key = [
			range.start.toISOString(),
			range.end.toISOString(),
			deviceId || 'network',
			chartTrafficTypes?.join(',') || 'all'
		].join('|');
		if (key === lastBandwidthKey) return;
		lastBandwidthKey = key;
		fetchBandwidth(range.start, range.end, deviceId, chartTrafficTypes);
	});

	function isValidRange(start: Date, end: Date): boolean {
		return start instanceof Date
			&& end instanceof Date
			&& Number.isFinite(start.getTime())
			&& Number.isFinite(end.getTime())
			&& start.getFullYear() > 1970
			&& end > start;
	}

	function clearBandwidth(reason: string) {
		if (bandwidthController) {
			bandwidthController.abort();
			bandwidthController = null;
		}
		chartData = [];
		rawTotals = { tx: 0, rx: 0 };
		emptyReason = reason;
		isLoading = false;
	}

	async function fetchBandwidth(start: Date, end: Date, nodeId: string | null, trafficTypes?: string[]): Promise<typeof chartData | null> {
		// Cancel previous in-flight request
		if (bandwidthController) {
			bandwidthController.abort();
		}
		bandwidthController = new AbortController();
		const signal = bandwidthController.signal;

		isLoading = true;
		try {
			const response = await tailscaleService.getBandwidth(start, end, nodeId || undefined, signal, trafficTypes);
			if (signal.aborted) return null;
			const bs = Math.max(response.metadata?.bucketSeconds || 60, 1);
			bucketSeconds = bs;
			// Compute raw totals before normalization
			const buckets = response.buckets || [];
			rawTotals = buckets.reduce(
				(acc, b) => ({ tx: acc.tx + b.txBytes, rx: acc.rx + b.rxBytes }),
				{ tx: 0, rx: 0 }
			);
			// Normalize to bytes/sec for chart display
			chartData = buckets
				.map((b) => ({
					time: new Date(b.time),
					txBytes: b.txBytes / bs,
					rxBytes: b.rxBytes / bs
				}))
				.sort((a, b) => a.time.getTime() - b.time.getTime());
			emptyReason = 'No bandwidth data in the selected range';
			return chartData;
		} catch (err) {
			if (signal.aborted) return null;
			console.error('Failed to fetch bandwidth:', err);
			chartData = [];
			rawTotals = { tx: 0, rx: 0 };
			emptyReason = 'Failed to load bandwidth data';
			return null;
		} finally {
			if (!signal.aborted) {
				isLoading = false;
			}
		}
	}

	// Calculate chart bounds from the full data range
	const chartBounds = $derived.by(() => {
		if (chartData.length === 0) {
			return { minTime: 0, maxTime: 1, maxBytes: 1 };
		}

		const times = chartData.map((d) => d.time.getTime());
		// For network total (no node selected), only use txBytes since rxBytes is 0
		// For per-node view, use both tx and rx
		const maxBytes = selectedNodeName
			? Math.max(...chartData.map((d) => Math.max(d.txBytes, d.rxBytes)), 1)
			: Math.max(...chartData.map((d) => d.txBytes), 1);

		return {
			minTime: Math.min(...times),
			maxTime: Math.max(...times),
			maxBytes: maxBytes * 1.1
		};
	});

	// Scale functions
	const chartWidth = $derived(containerWidth - padding.left - padding.right);
	const chartHeight = $derived(height - padding.top - padding.bottom);

	function scaleX(time: number): number {
		const { minTime, maxTime } = chartBounds;
		if (maxTime === minTime) return padding.left;
		return padding.left + ((time - minTime) / (maxTime - minTime)) * chartWidth;
	}

	function scaleY(bytes: number): number {
		const { maxBytes } = chartBounds;
		return padding.top + chartHeight - (bytes / maxBytes) * chartHeight;
	}

	// Generate SVG paths
	const txPath = $derived.by(() => {
		if (chartData.length === 0) return '';
		const points = chartData.map((d) => `${scaleX(d.time.getTime())},${scaleY(d.txBytes)}`);
		return `M${points.join('L')}`;
	});

	const rxPath = $derived.by(() => {
		if (chartData.length === 0) return '';
		const points = chartData.map((d) => `${scaleX(d.time.getTime())},${scaleY(d.rxBytes)}`);
		return `M${points.join('L')}`;
	});

	// Area paths (filled)
	const txArea = $derived.by(() => {
		if (chartData.length === 0) return '';
		const baseline = scaleY(0);
		const points = chartData.map((d) => `${scaleX(d.time.getTime())},${scaleY(d.txBytes)}`);
		const first = chartData[0];
		const last = chartData[chartData.length - 1];
		return `M${scaleX(first.time.getTime())},${baseline}L${points.join('L')}L${scaleX(last.time.getTime())},${baseline}Z`;
	});

	const rxArea = $derived.by(() => {
		if (chartData.length === 0) return '';
		const baseline = scaleY(0);
		const points = chartData.map((d) => `${scaleX(d.time.getTime())},${scaleY(d.rxBytes)}`);
		const first = chartData[0];
		const last = chartData[chartData.length - 1];
		return `M${scaleX(first.time.getTime())},${baseline}L${points.join('L')}L${scaleX(last.time.getTime())},${baseline}Z`;
	});

	// Selected window indicator
	const selectedRangeX = $derived.by(() => {
		const rangeStart = queryRange?.start ?? null;
		const rangeEnd = queryRange?.end ?? null;

		if (!rangeStart || !rangeEnd) return null;
		return {
			start: scaleX(rangeStart.getTime()),
			end: scaleX(rangeEnd.getTime())
		};
	});

	// Format time for axis - always show date for clarity
	function formatTime(time: Date): string {
		const { minTime, maxTime } = chartBounds;
		const rangeMs = maxTime - minTime;
		const hourMs = 60 * 60 * 1000;

		if (rangeMs > 2 * hourMs) {
			// Show date + time for ranges > 2 hours
			return time.toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) +
				' ' + time.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
		}
		// For short ranges, just show time
		return time.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
	}

	// Full totals use raw (un-normalized) byte counts for volume summary
	const fullTotals = $derived({ tx: rawTotals.tx, rx: rawTotals.rx, total: rawTotals.tx + rawTotals.rx });

	// Calculate totals for selected range (raw bytes, un-normalized)
	const totals = $derived.by(() => {
		const rangeStart = queryRange?.start ?? null;
		const rangeEnd = queryRange?.end ?? null;

		// Sum data within selected range (convert back from rate to raw bytes)
		if (rangeStart && rangeEnd) {
			const dataToSum = chartData.filter((d) => {
				const t = d.time.getTime();
				return t >= rangeStart!.getTime() && t <= rangeEnd!.getTime();
			});
			const tx = dataToSum.reduce((sum, d) => sum + d.txBytes * bucketSeconds, 0);
			const rx = dataToSum.reduce((sum, d) => sum + d.rxBytes * bucketSeconds, 0);
			return { tx, rx, total: tx + rx };
		}

		return fullTotals;
	});

	// Check if we're showing a subset of data
	const isShowingSubset = $derived(totals.total !== fullTotals.total);

	// Generate time axis ticks
	const timeAxisTicks = $derived.by(() => {
		if (chartData.length === 0) return [];
		const { minTime, maxTime } = chartBounds;
		const range = maxTime - minTime;
		const tickCount = Math.min(6, Math.max(2, Math.floor(chartWidth / 100)));
		const ticks: { x: number; label: string }[] = [];

		for (let i = 0; i <= tickCount; i++) {
			const time = minTime + (range * i) / tickCount;
			ticks.push({
				x: scaleX(time),
				label: formatTime(new Date(time))
			});
		}
		return ticks;
	});

	// Y-axis ticks (values are bytes/sec since chartData is normalized)
	const yAxisTicks = $derived.by(() => {
		const { maxBytes } = chartBounds;
		return [0, maxBytes / 2, maxBytes].map((bytesPerSec) => ({
			y: scaleY(bytesPerSec),
			label: formatBitsRate(bytesPerSec)
		}));
	});

	// Hover state for tooltip
	let hoverIndex = $state<number | null>(null);

	function handleChartMouseMove(e: MouseEvent) {
		if (chartData.length === 0) return;
		const svg = e.currentTarget as SVGElement;
		const rect = svg.getBoundingClientRect();
		const mouseX = e.clientX - rect.left;

		// Find closest data point by x position
		let closestIdx = 0;
		let closestDist = Infinity;
		for (let i = 0; i < chartData.length; i++) {
			const x = scaleX(chartData[i].time.getTime());
			const dist = Math.abs(x - mouseX);
			if (dist < closestDist) {
				closestDist = dist;
				closestIdx = i;
			}
		}
		hoverIndex = closestDist < 50 ? closestIdx : null;
	}

	function handleChartMouseLeave() {
		hoverIndex = null;
	}

	const hoverData = $derived.by(() => {
		if (hoverIndex === null || !chartData[hoverIndex]) return null;
		const d = chartData[hoverIndex];
		return {
			x: scaleX(d.time.getTime()),
			time: d.time,
			txRate: d.txBytes,
			rxRate: d.rxBytes,
			txRaw: d.txBytes * bucketSeconds,
			rxRaw: d.rxBytes * bucketSeconds
		};
	});
</script>

<div bind:this={container} class="relative w-full bg-card border-b border-border">
	<div class="flex items-center justify-between px-3 py-1.5">
		<div class="flex items-center gap-4">
			<span class="text-xs font-medium text-muted-foreground">
				{#if selectedNodeName}
					<span class="text-primary">{selectedNodeName}</span> Bandwidth
				{:else}
					Network Throughput
				{/if}
				{#if isShowingSubset}
					<span class="text-primary/70">
						(selected range)
					</span>
				{/if}
			</span>
			<div class="flex items-center gap-3 text-xs">
				{#if selectedNodeName}
					<!-- Per-node view: show TX/RX separately -->
					<span class="flex items-center gap-1">
						<span class="h-2 w-2 rounded-full bg-blue-500"></span>
						TX: {formatBytes(totals.tx)}
					</span>
					<span class="flex items-center gap-1">
						<span class="h-2 w-2 rounded-full bg-emerald-500"></span>
						RX: {formatBytes(totals.rx)}
					</span>
					<span class="text-muted-foreground">
						Total: {formatBytes(totals.total)}
					</span>
				{:else}
					<!-- Network total view: show single throughput value -->
					<span class="flex items-center gap-1">
						<span class="h-2 w-2 rounded-full bg-blue-500"></span>
						{formatBytes(totals.tx)}
					</span>
				{/if}
				{#if isShowingSubset}
					<span class="text-muted-foreground/60">
						| All: {formatBytes(fullTotals.tx)}
					</span>
				{/if}
			</div>
		</div>
		<div class="flex items-center gap-2">
			{#if hasActiveTrafficFilter}
				<span class="rounded bg-primary/10 px-1.5 py-0.5 text-[10px] text-primary" title={selectedNodeName ? 'Per-node bandwidth is not split by traffic type' : 'Network throughput follows the selected traffic types'}>
					{selectedNodeName ? 'node all types' : $filterStore.trafficTypes.join(', ')}
				</span>
			{/if}
			{#if chartData.length > 0}
				<span class="text-xs text-muted-foreground">
					{chartData.length} pts
				</span>
			{/if}
		</div>
	</div>

	{#if isLoading}
		<div class="flex items-center justify-center text-xs text-muted-foreground" style="height: {height}px">
			Loading bandwidth data...
		</div>
	{:else if chartData.length > 0}
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<svg
			width={containerWidth}
			{height}
			class="overflow-visible"
			onmousemove={handleChartMouseMove}
			onmouseleave={handleChartMouseLeave}
		>
			<!-- Grid lines -->
			<g class="text-border">
				{#each yAxisTicks as tick}
					<line
						x1={padding.left}
						y1={tick.y}
						x2={containerWidth - padding.right}
						y2={tick.y}
						stroke="currentColor"
						stroke-opacity="0.2"
						stroke-dasharray="2,2"
					/>
				{/each}
			</g>

			{#if selectedNodeName}
				<!-- Per-node view: show both TX and RX lines -->
				<path d={rxArea} fill="rgb(16, 185, 129)" fill-opacity="0.15" />
				<path d={rxPath} fill="none" stroke="rgb(16, 185, 129)" stroke-width="1.5" />
				<path d={txArea} fill="rgb(59, 130, 246)" fill-opacity="0.15" />
				<path d={txPath} fill="none" stroke="rgb(59, 130, 246)" stroke-width="1.5" />
			{:else}
				<!-- Network total view: show single throughput line -->
				<path d={txArea} fill="rgb(59, 130, 246)" fill-opacity="0.15" />
				<path d={txPath} fill="none" stroke="rgb(59, 130, 246)" stroke-width="1.5" />
			{/if}

			<!-- Selected range indicator -->
			{#if selectedRangeX !== null}
				{@const startX = Math.max(padding.left, selectedRangeX.start)}
				{@const endX = Math.min(containerWidth - padding.right, selectedRangeX.end)}
				{#if endX > startX}
					<rect
						x={startX}
						y={padding.top}
						width={endX - startX}
						height={chartHeight}
						fill="rgb(59, 130, 246)"
						fill-opacity="0.2"
						stroke="rgb(59, 130, 246)"
						stroke-width="2"
						stroke-opacity="0.6"
					/>
				{/if}
			{/if}

			<!-- Hover crosshair + tooltip -->
			{#if hoverData}
				<line
					x1={hoverData.x}
					y1={padding.top}
					x2={hoverData.x}
					y2={padding.top + chartHeight}
					stroke="currentColor"
					stroke-opacity="0.4"
					stroke-width="1"
					class="text-foreground"
				/>
				<!-- TX dot -->
				<circle
					cx={hoverData.x}
					cy={scaleY(hoverData.txRate)}
					r="3"
					fill="rgb(59, 130, 246)"
				/>
				{#if selectedNodeName}
					<!-- RX dot -->
					<circle
						cx={hoverData.x}
						cy={scaleY(hoverData.rxRate)}
						r="3"
						fill="rgb(16, 185, 129)"
					/>
				{/if}
			{/if}

			<!-- Y-axis labels -->
			{#each yAxisTicks as tick}
				<text
					x={padding.left - 4}
					y={tick.y}
					text-anchor="end"
					dominant-baseline="middle"
					class="fill-muted-foreground text-[9px]"
				>
					{tick.label}
				</text>
			{/each}

			<!-- X-axis labels -->
			{#each timeAxisTicks as tick}
				<text
					x={tick.x}
					y={height - 4}
					text-anchor="middle"
					class="fill-muted-foreground text-[9px]"
				>
					{tick.label}
				</text>
			{/each}
		</svg>

		<!-- Hover tooltip (floats above chart content) -->
		{#if hoverData}
			{@const tooltipX = Math.min(Math.max(hoverData.x, 80), containerWidth - 80)}
			<div
				class="pointer-events-none absolute z-10 -translate-x-1/2 whitespace-nowrap rounded border border-border bg-popover/95 px-2 py-1 text-[10px] shadow-md backdrop-blur-sm"
				style="left: {tooltipX}px; top: 30px;"
			>
				<div class="text-muted-foreground">{formatTime(hoverData.time)}</div>
				{#if selectedNodeName}
					<div class="flex items-center gap-1.5">
						<span class="h-1.5 w-1.5 rounded-full bg-blue-500"></span>
						TX: {formatBitsRate(hoverData.txRate)}
						<span class="text-muted-foreground/60">({formatBytes(hoverData.txRaw)})</span>
					</div>
					<div class="flex items-center gap-1.5">
						<span class="h-1.5 w-1.5 rounded-full bg-emerald-500"></span>
						RX: {formatBitsRate(hoverData.rxRate)}
						<span class="text-muted-foreground/60">({formatBytes(hoverData.rxRaw)})</span>
					</div>
				{:else}
					<div class="flex items-center gap-1.5">
						<span class="h-1.5 w-1.5 rounded-full bg-blue-500"></span>
						{formatBitsRate(hoverData.txRate)}
						<span class="text-muted-foreground/60">({formatBytes(hoverData.txRaw)})</span>
					</div>
				{/if}
			</div>
		{/if}
	{:else}
		<div class="flex items-center justify-center text-xs text-muted-foreground" style="height: {height}px">
			{#if selectedNode?.isVIPService}
				VIP service — per-service bandwidth tracking not yet available
			{:else}
				{emptyReason}
			{/if}
		</div>
	{/if}
</div>
