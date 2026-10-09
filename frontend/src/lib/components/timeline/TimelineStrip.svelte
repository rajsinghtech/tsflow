<script lang="ts">
	import { dataSourceStore } from '#lib/stores/data-source-store';
	import { commitIntent } from '#lib/stores/time-range-history';
	import { trafficPoints, trafficShapeError } from '#lib/stores/traffic-shape';
	import TrafficBrush from './TrafficBrush.svelte';
	import {
		binTraffic,
		resolveCoverage,
		timelineViewDomain
	} from './time-window';
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
	const mainBins = $derived(binTraffic(points, view.start, view.end, 96));
	const overviewBins = $derived(binTraffic(points, coverage.start, coverage.end, 120));
	const ready = $derived(coverage.end > coverage.start && !!source.selectedStart && !!source.selectedEnd);


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
