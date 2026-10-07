<script lang="ts">
	import { tailscaleService } from '#lib/services';
	import { dataSourceStore } from '#lib/stores/data-source-store';
	import { commitIntent } from '#lib/stores/time-range-history';
	import TrafficBrush from './TrafficBrush.svelte';
	import {
		binTraffic,
		resolveCoverage,
		sparklineWindows,
		timelineViewDomain,
		type TrafficPoint
	} from './time-window';
	import { formatStamp, type TimeZoneMode } from './time-range-url';

	let { zone }: { zone: TimeZoneMode } = $props();

	let points = $state<TrafficPoint[]>([]);
	let sparkNote = $state('');

	const source = $derived($dataSourceStore);
	const reportedStart = $derived(source.dataRange ? new Date(source.dataRange.earliest).getTime() : 0);
	const reportedEnd = $derived(source.dataRange ? new Date(source.dataRange.latest).getTime() : 0);
	const coverage = $derived(resolveCoverage(reportedStart, reportedEnd, points.map((point) => point.time)));
	const selectionStart = $derived(source.selectedStart?.getTime() ?? coverage.start);
	const selectionEnd = $derived(source.selectedEnd?.getTime() ?? coverage.end);
	const view = $derived(
		timelineViewDomain(selectionStart, selectionEnd, coverage.start, coverage.end, source.followLatest)
	);
	const mainBins = $derived(binTraffic(points, view.start, view.end, 96));
	const overviewBins = $derived(binTraffic(points, coverage.start, coverage.end, 120));
	const ready = $derived(coverage.end > coverage.start && !!source.selectedStart && !!source.selectedEnd);

	$effect(() => {
		const range = source.dataRange;
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
				if (!controller.signal.aborted) points = next;
			})
			.catch((err) => {
				if (controller.signal.aborted) return;
				console.error('Failed to load traffic overview:', err);
				sparkNote = 'Traffic shape unavailable';
				points = [];
			});
		return () => controller.abort();
	});

	async function loadPoints(start: number, end: number, signal: AbortSignal): Promise<TrafficPoint[]> {
		const windows = sparklineWindows(start, end);
		const responses = await Promise.all(
			windows.map((window) =>
				tailscaleService.getBandwidth(new Date(window.start), new Date(window.end), undefined, signal)
			)
		);
		const merged = new Map<number, TrafficPoint>();
		for (const response of responses) {
			const durationMs = Math.max(60_000, (response.metadata?.bucketSeconds || 3600) * 1000);
			for (const bucket of response.buckets || []) {
				const time = new Date(bucket.time).getTime();
				if (!Number.isFinite(time)) continue;
				const existing = merged.get(time);
				const bytes = bucket.txBytes + bucket.rxBytes;
				merged.set(time, {
					time,
					bytes: (existing?.bytes || 0) + bytes,
					durationMs: existing?.durationMs ? Math.min(existing.durationMs, durationMs) : durationMs
				});
			}
		}
		return [...merged.values()].sort((a, b) => a.time - b.time);
	}

	function commit(start: number, end: number) {
		commitIntent({ kind: 'absolute', start: new Date(start), end: new Date(end) }, zone);
	}
</script>

<div class="space-y-1 px-2 pb-2">
	{#if ready}
		<div class="flex items-center justify-between text-[11px] text-muted-foreground">
			<span>{formatStamp(new Date(view.start), zone)}</span>
			<span class="sr-only">Hatched bands are gaps with no data, not zero traffic.</span>
			<span>{formatStamp(new Date(view.end), zone)}</span>
		</div>
		<TrafficBrush
			bins={mainBins}
			domainStart={view.start}
			domainEnd={view.end}
			{selectionStart}
			{selectionEnd}
			onChange={commit}
			label="Traffic timeline"
		/>
		<div class="pt-1">
			<div class="mb-0.5 text-[10px] text-muted-foreground">Coverage</div>
			<TrafficBrush
				bins={overviewBins}
				domainStart={coverage.start}
				domainEnd={coverage.end}
				{selectionStart}
				{selectionEnd}
				onChange={commit}
				label="Whole coverage"
				tall={false}
			/>
		</div>
	{:else}
		<p class="py-3 text-xs text-muted-foreground">Collecting aggregate data...</p>
	{/if}
	{#if sparkNote}
		<p class="text-[11px] text-muted-foreground">{sparkNote}</p>
	{/if}
</div>
