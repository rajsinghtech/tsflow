<script lang="ts">
	import { ArrowLeft, ArrowRight, CalendarClock, Clock } from 'lucide-svelte';
	import { onMount } from 'svelte';
	import LiveStatus from '#lib/components/layout/LiveStatus.svelte';
	import { tailscaleService } from '#lib/services';
	import { dataSourceStore, hasStoredData } from '#lib/stores/data-source-store';
	import { lastUpdated, loadNetworkData } from '#lib/stores/network-store';
	import { resumeLive } from '#lib/stores/live-mode';
	import TrafficBrush from './TrafficBrush.svelte';
	import {
		ALL_LIVE_MS,
		DEFAULT_WINDOW_MS,
		WINDOW_PRESETS,
		binTraffic,
		clampWindow,
		formatStamp,
		formatWindow,
		liveRefreshEvery,
		matchingPreset,
		overviewDomain,
		resolveCoverage,
		sparklineWindows,
		validateWindow,
		windowSummary,
		zoomForWindow,
		type OverviewZoom,
		type TrafficPoint
	} from './time-window';

	let { onWindowChange = loadNetworkData }: { onWindowChange?: () => void | Promise<void> } = $props();

	const ZOOMS: { id: OverviewZoom; label: string }[] = [
		{ id: '24h', label: '24h' },
		{ id: '7d', label: '7d' },
		{ id: 'all', label: 'All' }
	];

	let reloadTimeout: ReturnType<typeof setTimeout> | null = null;
	let overview = $state<OverviewZoom>('24h');
	let points = $state<TrafficPoint[]>([]);
	let sparkNote = $state('');
	let refreshing = $state(false);
	let timeError = $state('');
	let startDraft = $state('');
	let endDraft = $state('');
	let startFocused = $state(false);
	let endFocused = $state(false);

	const dataRange = $derived($dataSourceStore.dataRange);
	const pollerStatus = $derived($dataSourceStore.pollerStatus);
	const selectedStart = $derived($dataSourceStore.selectedStart);
	const selectedEnd = $derived($dataSourceStore.selectedEnd);
	const followLatest = $derived($dataSourceStore.followLatest);
	const hasRange = $derived($hasStoredData && !!dataRange && !!selectedStart && !!selectedEnd);
	const reportedStart = $derived(dataRange ? new Date(dataRange.earliest).getTime() : 0);
	const reportedEnd = $derived(dataRange ? new Date(dataRange.latest).getTime() : 0);
	const coverage = $derived(resolveCoverage(reportedStart, reportedEnd, points.map((point) => point.time)));
	const domain = $derived(overviewDomain(coverage.start, coverage.end, overview));
	const windowMs = $derived(
		selectedStart && selectedEnd ? Math.max(0, selectedEnd.getTime() - selectedStart.getTime()) : 0
	);
	const activePreset = $derived(matchingPreset($dataSourceStore.latestWindowMs, followLatest));
	const summary = $derived(
		selectedStart && selectedEnd
			? windowSummary(selectedStart.getTime(), selectedEnd.getTime(), coverage.start, coverage.end)
			: ''
	);
	const bins = $derived(binTraffic(points, domain.start, domain.end, 48));
	const selectionOutside = $derived(
		!!selectedStart &&
			!!selectedEnd &&
			(selectedEnd.getTime() <= domain.start || selectedStart.getTime() >= domain.end)
	);
	const extendsPastOverview = $derived(
		!!selectedStart &&
			!!selectedEnd &&
			!selectionOutside &&
			(selectedStart.getTime() < domain.start || selectedEnd.getTime() > domain.end)
	);
	const everyLabel = $derived(liveRefreshEvery(pollerStatus?.pollInterval));

	onMount(() => {
		Promise.all([dataSourceStore.fetchDataRange(), dataSourceStore.fetchPollerStatus()]);
		const interval = setInterval(() => {
			dataSourceStore.fetchPollerStatus();
			if ($dataSourceStore.followLatest) dataSourceStore.fetchDataRange();
		}, 30_000);
		return () => {
			clearInterval(interval);
			if (reloadTimeout) clearTimeout(reloadTimeout);
		};
	});

	$effect(() => {
		const range = dataRange;
		if (!range?.earliest || !range.latest) {
			points = [];
			return;
		}
		const start = new Date(range.earliest).getTime();
		const end = new Date(range.latest).getTime();
		if (!(end > start)) return;
		const controller = new AbortController();
		sparkNote = '';
		void loadPoints(start, end, controller.signal)
			.then((next) => {
				if (controller.signal.aborted) return;
				points = next;
			})
			.catch((err) => {
				if (controller.signal.aborted) return;
				console.error('Failed to load traffic overview:', err);
				sparkNote = 'Traffic shape unavailable';
				points = [];
			});
		return () => controller.abort();
	});

	$effect(() => {
		if (!startFocused) startDraft = formatInputValue(selectedStart);
		if (!endFocused) endDraft = formatInputValue(selectedEnd);
	});

	async function loadPoints(start: number, end: number, signal: AbortSignal): Promise<TrafficPoint[]> {
		const windows = sparklineWindows(start, end);
		const responses = await Promise.all(
			windows.map((window) =>
				tailscaleService.getBandwidth(new Date(window.start), new Date(window.end), undefined, signal)
			)
		);
		const merged = new Map<number, number>();
		for (const response of responses) {
			for (const bucket of response.buckets || []) {
				const time = new Date(bucket.time).getTime();
				if (!Number.isFinite(time)) continue;
				merged.set(time, (merged.get(time) || 0) + bucket.txBytes + bucket.rxBytes);
			}
		}
		return [...merged.entries()]
			.map(([time, bytes]) => ({ time, bytes }))
			.sort((a, b) => a.time - b.time);
	}

	function scheduleReload() {
		if (reloadTimeout) clearTimeout(reloadTimeout);
		reloadTimeout = setTimeout(() => {
			onWindowChange();
		}, 200);
	}

	function commitRange(startMs: number, endMs: number) {
		if (!dataRange) return;
		const bounded = clampWindow(startMs, endMs, coverage.start, coverage.end);
		timeError = '';
		dataSourceStore.setSelectedRange(new Date(bounded.start), new Date(bounded.end));
		scheduleReload();
	}

	function choosePreset(ms: number) {
		if (!dataRange) return;
		overview = zoomForWindow(ms);
		dataSourceStore.showLatestWindow(dataRange, ms);
		timeError = '';
		scheduleReload();
	}

	function chooseAll() {
		if (!dataRange) return;
		overview = 'all';
		dataSourceStore.showLatestWindow(dataRange, ALL_LIVE_MS);
		timeError = '';
		scheduleReload();
	}

	function backToLive() {
		if (!dataRange) return;
		const ms = $dataSourceStore.latestWindowMs || DEFAULT_WINDOW_MS;
		overview = zoomForWindow(ms);
		resumeLive();
		timeError = '';
		scheduleReload();
	}

	async function refreshNow() {
		refreshing = true;
		try {
			await onWindowChange();
		} finally {
			refreshing = false;
		}
	}

	function shiftWindow(direction: -1 | 1) {
		if (!selectedStart || !selectedEnd) return;
		const width = selectedEnd.getTime() - selectedStart.getTime();
		commitRange(selectedStart.getTime() + width * direction, selectedEnd.getTime() + width * direction);
	}

	function applyDraft(which: 'start' | 'end') {
		if (!selectedStart || !selectedEnd) return;
		const nextStart = which === 'start' ? parseLocalInput(startDraft) : selectedStart;
		const nextEnd = which === 'end' ? parseLocalInput(endDraft) : selectedEnd;
		const error = validateWindow(nextStart, nextEnd, coverage.start, coverage.end);
		if (error || !nextStart || !nextEnd) {
			timeError = error ?? 'Enter a valid start and end.';
			if (which === 'start') startDraft = formatInputValue(selectedStart);
			else endDraft = formatInputValue(selectedEnd);
			return;
		}
		if (
			Math.abs(nextStart.getTime() - selectedStart.getTime()) < 1000 &&
			Math.abs(nextEnd.getTime() - selectedEnd.getTime()) < 1000
		) {
			return;
		}
		timeError = '';
		commitRange(nextStart.getTime(), nextEnd.getTime());
	}

	function formatInputValue(date: Date | null): string {
		if (!date) return '';
		const offsetMs = date.getTimezoneOffset() * 60_000;
		return new Date(date.getTime() - offsetMs).toISOString().slice(0, 16);
	}

	function parseLocalInput(value: string): Date | null {
		if (!value) return null;
		const parsed = new Date(value);
		return Number.isNaN(parsed.getTime()) ? null : parsed;
	}
</script>

<div class="space-y-3">
	<div class="flex items-center gap-1.5 text-sm font-medium">
		<CalendarClock class="h-4 w-4 text-muted-foreground" />
		<span>Time window</span>
	</div>

	<LiveStatus
		live={followLatest}
		windowLabel={formatWindow(windowMs)}
		updatedAt={$lastUpdated}
		{everyLabel}
		{refreshing}
		canReturn={!!$hasStoredData}
		onBackToLive={backToLive}
		onRefresh={refreshNow}
	/>

	{#if hasRange && dataRange && selectedStart && selectedEnd}
		<div class="space-y-3 rounded-md border border-border bg-muted/30 p-3">
			<div class="grid grid-cols-4 gap-1" role="group" aria-label="Latest windows">
				{#each WINDOW_PRESETS as preset}
					<button
						type="button"
						onclick={() => choosePreset(preset.ms)}
						aria-pressed={activePreset === preset.label}
						class="min-h-8 rounded-md border border-border px-2 text-xs hover:bg-secondary focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)]"
						class:bg-secondary={activePreset === preset.label}
						class:border-primary={activePreset === preset.label}
					>
						{preset.label}
					</button>
				{/each}
				<button
					type="button"
					onclick={chooseAll}
					aria-pressed={activePreset === 'All'}
					class="min-h-8 rounded-md border border-border px-2 text-xs hover:bg-secondary focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)]"
					class:bg-secondary={activePreset === 'All'}
					class:border-primary={activePreset === 'All'}
				>
					All
				</button>
			</div>

			<div class="flex items-center justify-between gap-2">
				<span class="text-xs text-muted-foreground" id="overview-zoom-label">Overview</span>
				<div class="flex gap-1" role="group" aria-labelledby="overview-zoom-label">
					{#each ZOOMS as zoom}
						<button
							type="button"
							onclick={() => (overview = zoom.id)}
							aria-pressed={overview === zoom.id}
							class="min-h-7 rounded-md border border-border px-2 text-xs hover:bg-secondary focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)]"
							class:bg-secondary={overview === zoom.id}
							class:border-primary={overview === zoom.id}
						>
							{zoom.label}
						</button>
					{/each}
				</div>
			</div>

			<div class="space-y-1">
				<div class="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
					<span>{formatStamp(domain.start)}</span>
					<span>{formatStamp(domain.end)}</span>
				</div>
				<TrafficBrush
					{bins}
					domainStart={domain.start}
					domainEnd={domain.end}
					selectionStart={selectedStart.getTime()}
					selectionEnd={selectedEnd.getTime()}
					onChange={commitRange}
				/>
				{#if sparkNote}
					<p class="text-[11px] text-muted-foreground">{sparkNote}</p>
				{/if}
				{#if selectionOutside}
					<button
						type="button"
						class="text-left text-[11px] text-primary hover:underline"
						onclick={() => (overview = 'all')}
					>
						This window is outside the overview. Show all coverage.
					</button>
				{:else if extendsPastOverview}
					<p class="text-[11px] text-muted-foreground">The window continues past this overview.</p>
				{/if}
			</div>

			<div class="grid grid-cols-[auto_1fr_auto] items-end gap-2">
				<button
					type="button"
					onclick={() => shiftWindow(-1)}
					class="flex h-10 w-10 items-center justify-center rounded-md border border-border hover:bg-secondary focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)]"
					title="Previous window"
					aria-label="Previous time window"
				>
					<ArrowLeft class="h-4 w-4" />
				</button>

				<div class="grid grid-cols-1 gap-2">
					<label class="space-y-1">
						<span class="text-xs text-muted-foreground">Start</span>
						<input
							type="datetime-local"
							aria-label="Start"
							aria-invalid={timeError ? 'true' : undefined}
							aria-describedby={timeError ? 'time-window-error' : 'time-window-summary'}
							class="h-10 w-full rounded-md border border-input bg-background px-2 text-xs"
							bind:value={startDraft}
							min={formatInputValue(new Date(coverage.start))}
							max={formatInputValue(selectedEnd)}
							onfocus={() => (startFocused = true)}
							onblur={() => {
								startFocused = false;
								applyDraft('start');
							}}
							onchange={() => applyDraft('start')}
						/>
					</label>
					<label class="space-y-1">
						<span class="text-xs text-muted-foreground">End</span>
						<input
							type="datetime-local"
							aria-label="End"
							aria-invalid={timeError ? 'true' : undefined}
							aria-describedby={timeError ? 'time-window-error' : 'time-window-summary'}
							class="h-10 w-full rounded-md border border-input bg-background px-2 text-xs"
							bind:value={endDraft}
							min={formatInputValue(selectedStart)}
							max={formatInputValue(new Date(coverage.end))}
							onfocus={() => (endFocused = true)}
							onblur={() => {
								endFocused = false;
								applyDraft('end');
							}}
							onchange={() => applyDraft('end')}
						/>
					</label>
				</div>

				<button
					type="button"
					onclick={() => shiftWindow(1)}
					disabled={followLatest || (!!selectedEnd && selectedEnd.getTime() >= coverage.end - 1000)}
					class="flex h-10 w-10 items-center justify-center rounded-md border border-border hover:bg-secondary focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)] disabled:cursor-not-allowed disabled:opacity-50"
					title="Next window"
					aria-label="Next time window"
				>
					<ArrowRight class="h-4 w-4" />
				</button>
			</div>

			<p id="time-window-summary" class="text-xs leading-snug text-muted-foreground">{summary}</p>
			{#if timeError}
				<p id="time-window-error" class="text-xs text-destructive" role="alert">{timeError}</p>
			{/if}
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
					<span class="font-mono">{formatStamp(new Date(pollerStatus.lastPollTime))}</span>
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
