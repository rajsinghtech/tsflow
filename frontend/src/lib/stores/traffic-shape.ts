import { derived, get, writable } from 'svelte/store';
import { tailscaleService } from '#lib/services';
import { resolveCoverage, sparklineWindows, type TrafficPoint } from '#lib/components/timeline/time-window';
import { dataSpans, rangeCoverage, type RangeCoverage, type Span } from '#lib/components/timeline/range-coverage';
import { dataSourceStore } from './data-source-store';
import { selectedTailnetId } from './tailnet-store';

// Hourly traffic over the whole stored range. It draws the timeline and tells
// the header, picker and empty states which parts of a window have data.
// null until loaded, or when the overview failed.
export const trafficPoints = writable<TrafficPoint[] | null>(null);
export const trafficShapeError = writable('');

let loadedKey = '';
let controller: AbortController | null = null;

function shapeKey(earliest: string, latest: string): string {
	return `${get(selectedTailnetId) ?? ''}|${earliest}|${latest}`;
}

// Loads the overview once per stored range. Pages share it, so moving between
// tabs does not fetch it again.
export function loadTrafficShape(earliest: string | undefined, latest: string | undefined): void {
	if (!earliest || !latest) {
		controller?.abort();
		controller = null;
		loadedKey = '';
		trafficPoints.set(null);
		return;
	}
	const start = new Date(earliest).getTime();
	const end = new Date(latest).getTime();
	if (!(end > start)) return;
	const key = shapeKey(earliest, latest);
	if (key === loadedKey) return;
	loadedKey = key;
	controller?.abort();
	const current = new AbortController();
	controller = current;
	trafficShapeError.set('');
	void fetchPoints(start, end, current.signal)
		.then((points) => {
			if (!current.signal.aborted) trafficPoints.set(points);
		})
		.catch((err) => {
			if (current.signal.aborted) return;
			console.error('Failed to load traffic overview:', err);
			trafficShapeError.set('Traffic shape unavailable');
			trafficPoints.set(null);
			loadedKey = '';
		});
}

async function fetchPoints(start: number, end: number, signal: AbortSignal): Promise<TrafficPoint[]> {
	const windows = sparklineWindows(start, end);
	const responses = await Promise.all(
		windows.map((window) => tailscaleService.getBandwidth(new Date(window.start), new Date(window.end), undefined, signal))
	);
	const merged = new Map<number, TrafficPoint>();
	for (const response of responses) {
		const durationMs = Math.max(60_000, (response.metadata?.bucketSeconds || 3600) * 1000);
		for (const bucket of response.buckets || []) {
			const time = new Date(bucket.time).getTime();
			if (!Number.isFinite(time)) continue;
			const existing = merged.get(time);
			merged.set(time, {
				time,
				bytes: (existing?.bytes || 0) + bucket.txBytes + bucket.rxBytes,
				durationMs: existing?.durationMs ? Math.min(existing.durationMs, durationMs) : durationMs
			});
		}
	}
	return [...merged.values()].sort((a, b) => a.time - b.time);
}

// The stored range, trimmed of stray early buckets the same way the timeline is.
export const storedCoverage = derived([dataSourceStore, trafficPoints], ([$source, $points]): Span | null => {
	const range = $source.dataRange;
	if (!range?.earliest || !range.latest) return null;
	const start = new Date(range.earliest).getTime();
	const end = new Date(range.latest).getTime();
	if (!(end > start)) return null;
	return resolveCoverage(start, end, ($points ?? []).map((point) => point.time));
});

export const storedSpans = derived(trafficPoints, ($points) => ($points ? dataSpans($points) : null));

// How much of the selected window has stored data.
export const windowCoverage = derived(
	[dataSourceStore, storedCoverage, storedSpans],
	([$source, $coverage, $spans]): RangeCoverage => {
		const start = $source.selectedStart?.getTime();
		const end = $source.selectedEnd?.getTime();
		if (start === undefined || end === undefined) return { kind: 'full' };
		return rangeCoverage(start, end, $coverage, $spans);
	}
);

export function resetTrafficShapeForTests(): void {
	controller?.abort();
	controller = null;
	loadedKey = '';
	trafficPoints.set(null);
	trafficShapeError.set('');
}
