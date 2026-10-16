import { get, writable } from 'svelte/store';
import { tailscaleService } from '#lib/services';
import type { TrafficPoint } from '#lib/components/timeline/time-window';
import { selectedTailnetId } from './tailnet-store';

// Per-minute traffic for the time bar chart when it shows a short span. The
// hourly overview is too coarse to read a rate off a few hours, and the
// backend only returns minute buckets for windows of two hours or less, so
// the span is fetched in fixed two-hour slices. Finished slices never change
// and are cached; only the slice holding the newest data is fetched again.
// Minute buckets come from the per-tailnet bandwidth table (one row per
// minute, whatever the node count), so a slice is cheap even at 20k nodes.

export const DETAIL_CHUNK_MS = 2 * 60 * 60 * 1000;
export const DETAIL_MAX_SPAN_MS = 48 * 60 * 60 * 1000;
const MAX_CACHED = 48;

export interface TrafficDetail {
	start: number;
	end: number;
	points: TrafficPoint[];
}

export const trafficDetail = writable<TrafficDetail | null>(null);

const cache = new Map<string, TrafficPoint[]>();
let cacheTailnet: string | null | undefined;
let requestKey = '';
let controller: AbortController | null = null;

export function detailSlices(start: number, end: number, dataEnd: number): { start: number; end: number }[] {
	if (!(end > start)) return [];
	const slices: { start: number; end: number }[] = [];
	for (let slice = Math.floor(start / DETAIL_CHUNK_MS) * DETAIL_CHUNK_MS; slice < end; slice += DETAIL_CHUNK_MS) {
		const sliceEnd = Math.min(slice + DETAIL_CHUNK_MS, dataEnd);
		if (sliceEnd > slice) slices.push({ start: slice, end: sliceEnd });
	}
	return slices;
}

async function fetchSlice(start: number, end: number, signal: AbortSignal): Promise<TrafficPoint[]> {
	const response = await tailscaleService.getBandwidth(new Date(start), new Date(end), undefined, signal);
	const durationMs = Math.max(60_000, (response.metadata?.bucketSeconds || 60) * 1000);
	const points: TrafficPoint[] = [];
	for (const bucket of response.buckets || []) {
		const time = new Date(bucket.time).getTime();
		if (!Number.isFinite(time)) continue;
		points.push({
			time,
			bytes: bucket.txBytes + bucket.rxBytes,
			durationMs,
			coveredMs: bucket.seconds && bucket.seconds > 0 ? bucket.seconds * 1000 : durationMs
		});
	}
	return points;
}

// Loads minute data for [start, end]. Spans longer than DETAIL_MAX_SPAN_MS
// clear the detail and the chart falls back to the hourly overview.
export function loadTrafficDetail(start: number, end: number, dataEnd: number): void {
	const tailnet = get(selectedTailnetId);
	if (tailnet !== cacheTailnet) {
		cache.clear();
		cacheTailnet = tailnet;
	}
	const span = end - start;
	if (!(span > 0) || span > DETAIL_MAX_SPAN_MS || !(dataEnd > start)) {
		controller?.abort();
		controller = null;
		requestKey = '';
		trafficDetail.set(null);
		return;
	}
	const slices = detailSlices(start, end, dataEnd);
	const key = `${tailnet ?? ''}|${slices.map((slice) => `${slice.start}-${slice.end}`).join(',')}`;
	if (key === requestKey) return;
	requestKey = key;
	controller?.abort();
	const current = new AbortController();
	controller = current;
	void Promise.all(
		slices.map(async (slice) => {
			const sliceKey = `${tailnet ?? ''}|${slice.start}|${slice.end}`;
			const hit = cache.get(sliceKey);
			if (hit) return hit;
			const points = await fetchSlice(slice.start, slice.end, current.signal);
			if (slice.end - slice.start === DETAIL_CHUNK_MS) {
				cache.set(sliceKey, points);
				if (cache.size > MAX_CACHED) cache.delete(cache.keys().next().value as string);
			}
			return points;
		})
	)
		.then((parts) => {
			if (current.signal.aborted) return;
			trafficDetail.set({
				start: slices[0].start,
				end: slices[slices.length - 1].end,
				points: parts.flat().sort((a, b) => a.time - b.time)
			});
		})
		.catch((err) => {
			if (current.signal.aborted) return;
			// The hourly overview still draws the chart, so this is not shown.
			console.warn('Failed to load minute traffic:', err);
			requestKey = '';
			trafficDetail.set(null);
		});
}

export function resetTrafficDetailForTests(): void {
	controller?.abort();
	controller = null;
	requestKey = '';
	cache.clear();
	cacheTailnet = undefined;
	trafficDetail.set(null);
}
