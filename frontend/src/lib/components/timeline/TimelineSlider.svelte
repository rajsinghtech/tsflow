<script lang="ts">
	import { ArrowLeft, ArrowRight, CalendarClock, Clock, MoveHorizontal, RotateCcw } from 'lucide-svelte';
	import { onMount } from 'svelte';
	import { dataSourceStore, hasStoredData } from '$lib/stores/data-source-store';
	import { loadNetworkData } from '$lib/stores';

	let { onWindowChange = loadNetworkData }: { onWindowChange?: () => void | Promise<void> } = $props();

	const SCALE = 10000;
	const MIN_WINDOW_MS = 5 * 60 * 1000;
	const presets = [
		{ label: '15m', ms: 15 * 60 * 1000 },
		{ label: '1h', ms: 60 * 60 * 1000 },
		{ label: '2h', ms: 2 * 60 * 60 * 1000 },
		{ label: '6h', ms: 6 * 60 * 60 * 1000 },
		{ label: '24h', ms: 24 * 60 * 60 * 1000 },
		{ label: '7d', ms: 7 * 24 * 60 * 60 * 1000 }
	];

	let reloadTimeout: ReturnType<typeof setTimeout> | null = null;
	let startValue = $state(0);
	let endValue = $state(SCALE);
	let trackEl: HTMLDivElement | null = $state(null);
	let dragMode: 'start' | 'end' | 'pan' | null = $state(null);
	let dragStartX = 0;
	let dragStartValue = 0;
	let dragEndValue = SCALE;
	let dragTarget: HTMLElement | null = null;
	let dragPointerId: number | null = null;

	const dataRange = $derived($dataSourceStore.dataRange);
	const pollerStatus = $derived($dataSourceStore.pollerStatus);
	const selectedStart = $derived($dataSourceStore.selectedStart);
	const selectedEnd = $derived($dataSourceStore.selectedEnd);
	const followLatest = $derived($dataSourceStore.followLatest);
	const hasRange = $derived($hasStoredData && !!dataRange && !!selectedStart && !!selectedEnd);

	const windowMs = $derived(
		selectedStart && selectedEnd ? Math.max(0, selectedEnd.getTime() - selectedStart.getTime()) : 0
	);
	const rangeMs = $derived.by(() => {
		if (!dataRange) return 0;
		return new Date(dataRange.latest).getTime() - new Date(dataRange.earliest).getTime();
	});
	const activePreset = $derived.by(() => {
		if (!windowMs) return '';
		const match = presets.find((preset) => Math.abs(preset.ms - windowMs) < 60 * 1000);
		return match?.label ?? '';
	});

	onMount(() => {
		Promise.all([dataSourceStore.fetchDataRange(), dataSourceStore.fetchPollerStatus()]);

		const interval = setInterval(() => {
			dataSourceStore.fetchPollerStatus();
			if ($dataSourceStore.followLatest) {
				dataSourceStore.fetchDataRange();
			}
		}, 30000);

		return () => {
			clearInterval(interval);
			if (reloadTimeout) clearTimeout(reloadTimeout);
			detachDragListeners();
		};
	});

	$effect(() => {
		if (dragMode) return;
		if (!dataRange || !selectedStart || !selectedEnd) return;
		const earliest = new Date(dataRange.earliest).getTime();
		const latest = new Date(dataRange.latest).getTime();
		const span = latest - earliest;
		if (span <= 0) return;
		const nextStartValue = clampValue(((selectedStart.getTime() - earliest) / span) * SCALE);
		let nextEndValue = clampValue(((selectedEnd.getTime() - earliest) / span) * SCALE);
		if (nextEndValue <= nextStartValue) {
			nextEndValue = clampValue(nextStartValue + minValueGap());
		}
		startValue = nextStartValue;
		endValue = nextEndValue;
	});

	function scheduleReload() {
		if (reloadTimeout) clearTimeout(reloadTimeout);
		reloadTimeout = setTimeout(() => {
			onWindowChange();
		}, 200);
	}

	function commitRange(start: Date, end: Date, reload = true) {
		if (!dataRange) return;
		const bounded = clampDates(start, end);
		dataSourceStore.setSelectedRange(bounded.start, bounded.end);
		if (reload) scheduleReload();
	}

	function commitValues(reload = true) {
		const start = valueToTime(startValue);
		const end = valueToTime(endValue);
		if (!start || !end || end <= start) return;
		commitRange(start, end, reload);
	}

	function clampDates(start: Date, end: Date): { start: Date; end: Date } {
		if (!dataRange) return { start, end };
		const earliest = new Date(dataRange.earliest).getTime();
		const latest = new Date(dataRange.latest).getTime();
		let startMs = start.getTime();
		let endMs = end.getTime();
		let duration = Math.max(MIN_WINDOW_MS, endMs - startMs);
		duration = Math.min(duration, Math.max(MIN_WINDOW_MS, latest - earliest));

		if (endMs > latest) {
			endMs = latest;
			startMs = endMs - duration;
		}
		if (startMs < earliest) {
			startMs = earliest;
			endMs = startMs + duration;
		}
		if (endMs > latest) endMs = latest;

		return { start: new Date(startMs), end: new Date(endMs) };
	}

	function clampValue(value: number): number {
		return Math.max(0, Math.min(SCALE, value));
	}

	function minValueGap(): number {
		if (!rangeMs) return 1;
		return Math.max(1, (MIN_WINDOW_MS / rangeMs) * SCALE);
	}

	function valueToTime(value: number): Date | null {
		if (!dataRange) return null;
		const earliest = new Date(dataRange.earliest).getTime();
		const latest = new Date(dataRange.latest).getTime();
		return new Date(earliest + (latest - earliest) * (value / SCALE));
	}

	function setLatest(ms = windowMs || 2 * 60 * 60 * 1000) {
		if (!dataRange) return;
		dataSourceStore.showLatestWindow(dataRange, ms);
		scheduleReload();
	}

	function setPreset(ms: number) {
		setLatest(ms);
	}

	function showAll() {
		if (!dataRange) return;
		commitRange(new Date(dataRange.earliest), new Date(dataRange.latest));
	}

	function shiftWindow(direction: -1 | 1) {
		if (!selectedStart || !selectedEnd || !windowMs) return;
		const delta = windowMs * direction;
		commitRange(
			new Date(selectedStart.getTime() + delta),
			new Date(selectedEnd.getTime() + delta)
		);
	}

	function applyStartInput(value: string) {
		if (!selectedEnd) return;
		const next = parseLocalInput(value);
		if (next) commitRange(next, selectedEnd);
	}

	function applyEndInput(value: string) {
		if (!selectedStart) return;
		const next = parseLocalInput(value);
		if (next) commitRange(selectedStart, next);
	}

	function beginDrag(mode: 'start' | 'end' | 'pan', e: PointerEvent) {
		if (!trackEl) return;
		e.preventDefault();
		dragMode = mode;
		dragTarget = e.currentTarget as HTMLElement;
		dragPointerId = e.pointerId;
		dragStartX = e.clientX;
		dragStartValue = startValue;
		dragEndValue = endValue;
		dragTarget.setPointerCapture(e.pointerId);
		attachPointerDragListeners();
	}

	function beginMouseDrag(mode: 'start' | 'end' | 'pan', e: MouseEvent) {
		if (dragMode || !trackEl) return;
		e.preventDefault();
		dragMode = mode;
		dragStartX = e.clientX;
		dragStartValue = startValue;
		dragEndValue = endValue;
		attachMouseDragListeners();
	}

	function moveDrag(e: PointerEvent | MouseEvent) {
		if (!dragMode || !trackEl) return;
		const width = trackEl.getBoundingClientRect().width;
		if (width <= 0) return;

		const delta = ((e.clientX - dragStartX) / width) * SCALE;
		const minGap = minValueGap();

		if (dragMode === 'start') {
			startValue = Math.min(clampValue(dragStartValue + delta), endValue - minGap);
		} else if (dragMode === 'end') {
			endValue = Math.max(clampValue(dragEndValue + delta), startValue + minGap);
		} else {
			const windowWidth = dragEndValue - dragStartValue;
			const nextStart = Math.max(0, Math.min(SCALE - windowWidth, dragStartValue + delta));
			startValue = nextStart;
			endValue = nextStart + windowWidth;
		}
	}

	function endDrag(e: PointerEvent) {
		if (!dragMode) return;
		const pointerId = dragPointerId ?? e.pointerId;
		if (dragTarget?.hasPointerCapture(pointerId)) {
			dragTarget.releasePointerCapture(pointerId);
		}
		dragMode = null;
		dragTarget = null;
		dragPointerId = null;
		detachDragListeners();
		commitValues();
	}

	function endMouseDrag() {
		if (!dragMode) return;
		dragMode = null;
		dragTarget = null;
		dragPointerId = null;
		detachDragListeners();
		commitValues();
	}

	function attachPointerDragListeners() {
		window.addEventListener('pointermove', moveDrag);
		window.addEventListener('pointerup', endDrag);
	}

	function attachMouseDragListeners() {
		window.addEventListener('mousemove', moveDrag);
		window.addEventListener('mouseup', endMouseDrag);
	}

	function detachDragListeners() {
		window.removeEventListener('pointermove', moveDrag);
		window.removeEventListener('pointerup', endDrag);
		window.removeEventListener('mousemove', moveDrag);
		window.removeEventListener('mouseup', endMouseDrag);
	}

	function formatDateTime(date: Date | string | null): string {
		if (!date) return '--';
		const d = typeof date === 'string' ? new Date(date) : date;
		return d.toLocaleString(undefined, {
			month: 'short',
			day: 'numeric',
			hour: '2-digit',
			minute: '2-digit'
		});
	}

	function formatDuration(ms: number): string {
		if (ms <= 0) return '--';
		const minutes = Math.round(ms / 60000);
		if (minutes < 60) return `${minutes}m`;
		const hours = minutes / 60;
		if (hours < 48) return `${Number.isInteger(hours) ? hours : hours.toFixed(1)}h`;
		const days = hours / 24;
		return `${Number.isInteger(days) ? days : days.toFixed(1)}d`;
	}

	function formatInputValue(date: Date | null): string {
		if (!date) return '';
		const offsetMs = date.getTimezoneOffset() * 60000;
		return new Date(date.getTime() - offsetMs).toISOString().slice(0, 16);
	}

	function parseLocalInput(value: string): Date | null {
		if (!value) return null;
		const parsed = new Date(value);
		return Number.isNaN(parsed.getTime()) ? null : parsed;
	}
</script>

<svelte:window onpointermove={moveDrag} onpointerup={endDrag} onmousemove={moveDrag} onmouseup={endMouseDrag} />

<div class="space-y-3">
	<div class="flex items-center justify-between gap-2">
		<div>
			<div class="flex items-center gap-1.5 text-sm font-medium">
				<CalendarClock class="h-4 w-4 text-muted-foreground" />
				<span>Time Window</span>
			</div>
			{#if hasRange}
				<p class="text-xs text-muted-foreground">
					{followLatest ? 'Following latest' : 'Custom'} · {formatDuration(windowMs)}
				</p>
			{/if}
		</div>
		<button
			type="button"
			onclick={() => setLatest()}
			disabled={!$hasStoredData || followLatest}
			class="inline-flex min-h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs hover:bg-secondary disabled:cursor-not-allowed disabled:opacity-50"
			title="Follow newest stored data"
		>
			<RotateCcw class="h-3.5 w-3.5" />
			Latest
		</button>
	</div>

	{#if hasRange && dataRange}
		<div class="space-y-3 rounded-md border border-border bg-muted/30 p-3">
			<div class="grid grid-cols-4 gap-1">
				{#each presets as preset}
					<button
						type="button"
						onclick={() => setPreset(preset.ms)}
						class="min-h-8 rounded-md border border-border px-2 text-xs hover:bg-secondary"
						class:bg-secondary={activePreset === preset.label && followLatest}
					>
						{preset.label}
					</button>
				{/each}
				<button
					type="button"
					onclick={showAll}
					class="min-h-8 rounded-md border border-border px-2 text-xs hover:bg-secondary"
				>
					All
				</button>
			</div>

			<div class="grid grid-cols-[auto_1fr_auto] items-end gap-2">
				<button
					type="button"
					onclick={() => shiftWindow(-1)}
					class="flex h-10 w-10 items-center justify-center rounded-md border border-border hover:bg-secondary"
					title="Previous window"
					aria-label="Previous time window"
				>
					<ArrowLeft class="h-4 w-4" />
				</button>

				<div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
					<label class="space-y-1">
						<span class="text-xs text-muted-foreground">Start</span>
						<input
							type="datetime-local"
							class="h-10 w-full rounded-md border border-input bg-background px-2 text-xs"
							value={formatInputValue(selectedStart)}
							min={formatInputValue(new Date(dataRange.earliest))}
							max={formatInputValue(selectedEnd)}
							onchange={(e) => applyStartInput(e.currentTarget.value)}
						/>
					</label>
					<label class="space-y-1">
						<span class="text-xs text-muted-foreground">End</span>
						<input
							type="datetime-local"
							class="h-10 w-full rounded-md border border-input bg-background px-2 text-xs"
							value={formatInputValue(selectedEnd)}
							min={formatInputValue(selectedStart)}
							max={formatInputValue(new Date(dataRange.latest))}
							onchange={(e) => applyEndInput(e.currentTarget.value)}
						/>
					</label>
				</div>

				<button
					type="button"
					onclick={() => shiftWindow(1)}
					disabled={followLatest}
					class="flex h-10 w-10 items-center justify-center rounded-md border border-border hover:bg-secondary disabled:cursor-not-allowed disabled:opacity-50"
					title="Next window"
					aria-label="Next time window"
				>
					<ArrowRight class="h-4 w-4" />
				</button>
			</div>

			<div class="space-y-1.5">
				<div class="flex items-center justify-between gap-2 text-xs text-muted-foreground">
					<span>{formatDateTime(dataRange.earliest)}</span>
					<span>{formatDateTime(dataRange.latest)}</span>
				</div>

				<div bind:this={trackEl} class="relative h-12 rounded-md bg-muted">
					<div
						class="absolute bottom-3 h-2 rounded-full bg-primary/35"
						style="left: {(startValue / SCALE) * 100}%; width: {((endValue - startValue) / SCALE) * 100}%"
					></div>

					<button
						type="button"
						class="absolute top-1 z-20 flex h-6 min-w-10 -translate-x-1/2 cursor-grab items-center justify-center rounded-md border border-border bg-background/95 shadow-sm active:cursor-grabbing"
						class:ring-2={dragMode === 'pan'}
						class:ring-primary={dragMode === 'pan'}
						style="left: {((startValue + endValue) / 2 / SCALE) * 100}%; width: max({((endValue - startValue) / SCALE) * 100}%, 2.5rem)"
						onpointerdown={(e) => beginDrag('pan', e)}
						onmousedown={(e) => beginMouseDrag('pan', e)}
						onpointercancel={endDrag}
						aria-label="Move selected time window"
						title="Move selected time window"
					>
						<MoveHorizontal class="h-3.5 w-3.5 text-muted-foreground" />
					</button>

					<button
						type="button"
						class="absolute bottom-0 z-30 h-8 w-4 -translate-x-1/2 cursor-ew-resize rounded-sm border border-background bg-primary shadow"
						class:ring-2={dragMode === 'start'}
						class:ring-primary={dragMode === 'start'}
						style="left: {(startValue / SCALE) * 100}%"
						onpointerdown={(e) => beginDrag('start', e)}
						onmousedown={(e) => beginMouseDrag('start', e)}
						onpointercancel={endDrag}
						aria-label="Resize start time"
						title="Resize start time"
					></button>
					<button
						type="button"
						class="absolute bottom-0 z-30 h-8 w-4 -translate-x-1/2 cursor-ew-resize rounded-sm border border-background bg-primary shadow"
						class:ring-2={dragMode === 'end'}
						class:ring-primary={dragMode === 'end'}
						style="left: {(endValue / SCALE) * 100}%"
						onpointerdown={(e) => beginDrag('end', e)}
						onmousedown={(e) => beginMouseDrag('end', e)}
						onpointercancel={endDrag}
						aria-label="Resize end time"
						title="Resize end time"
					></button>
				</div>
			</div>

			<div class="grid grid-cols-1 gap-1 text-xs text-muted-foreground">
				<div class="flex justify-between gap-2">
					<span>Selected</span>
					<span class="text-right font-mono text-foreground">{formatDateTime(selectedStart)} - {formatDateTime(selectedEnd)}</span>
				</div>
				<div class="flex justify-between gap-2">
					<span>Available</span>
					<span class="text-right font-mono">{formatDateTime(dataRange.earliest)} - {formatDateTime(dataRange.latest)}</span>
				</div>
			</div>
		</div>
	{:else}
		<div class="text-xs text-muted-foreground">
			<Clock class="mb-1 inline h-3 w-3" />
			Collecting aggregate data...
		</div>
	{/if}

	{#if pollerStatus}
		<div class="space-y-1 text-xs text-muted-foreground">
			<div class="flex justify-between">
				<span>Stored Records:</span>
				<span class="font-mono">{(pollerStatus.database?.dataRange?.count ?? pollerStatus.database?.tableCounts?.flow_logs_current ?? 0).toLocaleString()}</span>
			</div>
			{#if pollerStatus.lastPollTime && new Date(pollerStatus.lastPollTime).getFullYear() > 1970}
				<div class="flex justify-between">
					<span>Last Poll:</span>
					<span class="font-mono">{formatDateTime(pollerStatus.lastPollTime)}</span>
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
	{/if}
</div>
