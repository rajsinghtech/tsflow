<script lang="ts">
	import { dataSourceStore } from '#lib/stores/data-source-store';
	import { commitIntent } from '#lib/stores/time-range-history';
	import { trafficPoints, trafficShapeError } from '#lib/stores/traffic-shape';
	import { loadTrafficDetail, trafficDetail } from '#lib/stores/traffic-detail';
	import TrafficBrush from './TrafficBrush.svelte';
	import { byteBins, formatBps, peakBin, rateBins, slotLabel, windowRates } from './throughput';
	import {
		binTraffic,
		resolveCoverage,
		timelineViewDomain
	} from './time-window';
	import { pollFailure } from './poll-health';
	import { formatStamp, type TimeZoneMode } from './time-range-url';

	let { zone }: { zone: TimeZoneMode } = $props();

	const points = $derived($trafficPoints ?? []);
	const sparkNote = $derived($trafficShapeError);

	const source = $derived($dataSourceStore);
	const reportedStart = $derived(source.dataRange ? new Date(source.dataRange.earliest).getTime() : 0);
	const reportedEnd = $derived(source.dataRange ? new Date(source.dataRange.latest).getTime() : 0);
	const coverage = $derived(resolveCoverage(reportedStart, reportedEnd, points.map((point) => point.time)));
	const selectionStart = $derived(source.selectedStart?.getTime() ?? coverage.start);
	const selectionEnd = $derived(source.selectedEnd?.getTime() ?? coverage.end);
	const view = $derived(
		timelineViewDomain(selectionStart, selectionEnd, coverage.start, coverage.end, source.followLatest)
	);

	// The chart reads minute buckets when the view is short enough, otherwise
	// the hourly overview. Both are bucketed aggregates, never per-flow rows.
	$effect(() => {
		if (!ready) return;
		loadTrafficDetail(view.start, view.end, coverage.end);
	});
	const detail = $derived($trafficDetail);
	const chartPoints = $derived(
		detail && detail.start <= view.start + 60_000 && detail.end >= view.end - 60_000 ? detail.points : points
	);
	const rate = $derived(rateBins(chartPoints, view.start, view.end, 180));
	const mainBins = $derived(byteBins(rateBins(chartPoints, view.start, view.end, 96)));
	const readout = $derived(windowRates(chartPoints, selectionStart, selectionEnd));
	// Peak covers the whole strip, the same span the line and its marker show.
	const stripPeak = $derived(peakBin(rate));

	const UNIT_KEY = 'tsflow-timebar-unit';
	let unit = $state<'bits' | 'bytes'>(
		typeof localStorage !== 'undefined' && localStorage.getItem(UNIT_KEY) === 'bytes' ? 'bytes' : 'bits'
	);
	function setUnit(next: 'bits' | 'bytes') {
		unit = next;
		try {
			localStorage.setItem(UNIT_KEY, next);
		} catch {
			// Private mode. The choice just does not stick.
		}
	}
	const overviewBins = $derived(binTraffic(points, coverage.start, coverage.end, 120));
	const failing = $derived(!!pollFailure(source.pollerStatus));
	const ready = $derived(coverage.end > coverage.start && !!source.selectedStart && !!source.selectedEnd);


	function commit(start: number, end: number) {
		commitIntent({ kind: 'absolute', start: new Date(start), end: new Date(end) }, zone);
	}
</script>

<div class="space-y-1 overflow-x-clip px-2 pb-2">
	{#if ready}
		<div class="flex min-h-5 items-center justify-between gap-2 text-[11px] text-muted-foreground">
			{#if unit === 'bits'}
				<div class="flex min-w-0 items-baseline gap-3 tabular-nums" data-rate-readout="true">
					<span class="truncate">
						{source.followLatest ? 'Now' : 'Latest'}
						<span class="ml-0.5 text-[13px] font-semibold text-foreground">{readout.latest === null ? 'n/a' : formatBps(readout.latest)}</span>
					</span>
					<span class="truncate" title="Highest point on the chart">
						Peak
						<span class="ml-0.5 font-medium text-foreground">{stripPeak === null ? 'n/a' : formatBps(stripPeak.bps)}</span>
					</span>
				</div>
			{:else}
				<span>Bytes per {slotLabel((view.end - view.start) / 96)}</span>
			{/if}
			<span class="sr-only">Hatched bands are gaps with no data, not zero traffic.</span>
			<div class="flex shrink-0 items-center rounded-md bg-muted/60 p-0.5 text-[10px]" role="group" aria-label="Chart unit">
				{#each [['bits', 'bits/s'], ['bytes', 'bytes']] as [value, text]}
					<button
						type="button"
						class="rounded px-1.5 py-0.5 transition-colors pointer-coarse:px-2.5 pointer-coarse:py-1 {unit === value ? 'bg-background text-foreground shadow-sm' : 'hover:text-foreground'}"
						aria-pressed={unit === value}
						onclick={() => setUnit(value as 'bits' | 'bytes')}
					>{text}</button>
				{/each}
			</div>
		</div>
		<TrafficBrush
			bins={mainBins}
			domainStart={view.start}
			domainEnd={view.end}
			{selectionStart}
			{selectionEnd}
			onChange={commit}
			label="Traffic timeline"
			rate={unit === 'bits' ? rate : null}
			formatTime={(time) => formatStamp(new Date(time), zone)}
		/>
		<div class="flex items-center justify-between text-[10px] text-muted-foreground">
			<span>{formatStamp(new Date(view.start), zone)}</span>
			<span>{formatStamp(new Date(view.end), zone)}</span>
		</div>
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
		<p class="py-3 text-xs text-muted-foreground">
			{failing ? 'No data yet. The last poll failed.' : 'Collecting aggregate data...'}
		</p>
	{/if}
	{#if sparkNote}
		<p class="text-[11px] text-muted-foreground">{sparkNote}</p>
	{/if}
</div>
