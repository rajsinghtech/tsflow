import { refreshIntervalMs } from '#lib/utils/poll-interval';

export const MIN_WINDOW_MS = 5 * 60 * 1000;
export const DEFAULT_WINDOW_MS = 2 * 60 * 60 * 1000;
// A live "all" window grows when new data arrives instead of sliding forward.
export const ALL_LIVE_MS = 1000 * 365 * 24 * 60 * 60 * 1000;

export const COVERAGE_GAP_MS = 6 * 60 * 60 * 1000;
export const MAX_STRAY_SPAN_MS = 15 * 60 * 1000;
export const MAX_STRAY_BUCKETS = 20;
export const MAX_STRAY_SKIPS = 8;

export const HOURLY_CHUNK_MS = 48 * 60 * 60 * 1000;
export const MAX_SPARK_REQUESTS = 8;

export const WINDOW_PRESETS = [
	{ label: '15m', ms: 15 * 60 * 1000 },
	{ label: '1h', ms: 60 * 60 * 1000 },
	{ label: '2h', ms: 2 * 60 * 60 * 1000 },
	{ label: '6h', ms: 6 * 60 * 60 * 1000 },
	{ label: '24h', ms: 24 * 60 * 60 * 1000 },
	{ label: '7d', ms: 7 * 24 * 60 * 60 * 1000 }
] as const;

export type OverviewZoom = '24h' | '7d' | 'all';
export type BrushAction = 'start' | 'end' | 'move' | 'create';

export interface TrafficPoint {
	time: number;
	bytes: number;
}

export interface SparkBin {
	start: number;
	end: number;
	bytes: number;
}

// Same rule as the database coverage read: drop only a short early burst
// that is separated from the rest by at least six hours.
export function continuousCoverageStart(times: number[]): number | null {
	if (times.length === 0) return null;
	let startIdx = 0;
	for (let skip = 0; skip < MAX_STRAY_SKIPS; skip++) {
		let endIdx = startIdx;
		let splitIdx = -1;
		while (endIdx + 1 < times.length && endIdx - startIdx + 1 < MAX_STRAY_BUCKETS) {
			const next = endIdx + 1;
			const gap = times[next] - times[endIdx];
			const span = times[next] - times[startIdx];
			if (gap >= COVERAGE_GAP_MS || span > MAX_STRAY_SPAN_MS) {
				splitIdx = next;
				break;
			}
			endIdx = next;
		}
		if (splitIdx < 0) {
			if (endIdx + 1 >= times.length) return times[startIdx];
			splitIdx = endIdx + 1;
		}
		const gap = times[splitIdx] - times[endIdx];
		const span = times[endIdx] - times[startIdx];
		if (times[splitIdx] - times[startIdx] > MAX_STRAY_SPAN_MS && gap < COVERAGE_GAP_MS) {
			return times[startIdx];
		}
		if (gap >= COVERAGE_GAP_MS && span <= MAX_STRAY_SPAN_MS && endIdx - startIdx + 1 <= MAX_STRAY_BUCKETS) {
			startIdx = splitIdx;
			continue;
		}
		return times[startIdx];
	}
	return times[startIdx];
}

// reportedStart is what the range API returned. Bucket times trim it when
// this series actually reaches that start and the early points are stray.
export function resolveCoverage(
	reportedStart: number,
	reportedEnd: number,
	bucketTimes: number[]
): { start: number; end: number } {
	if (reportedEnd <= reportedStart) return { start: reportedStart, end: reportedEnd };
	if (bucketTimes.length === 0) return { start: reportedStart, end: reportedEnd };
	const sorted = [...bucketTimes].sort((a, b) => a - b);
	if (sorted[0] > reportedStart + COVERAGE_GAP_MS) {
		return { start: reportedStart, end: reportedEnd };
	}
	const trimmed = continuousCoverageStart(sorted);
	return { start: trimmed ?? reportedStart, end: reportedEnd };
}

export function overviewDomain(
	coverageStart: number,
	coverageEnd: number,
	zoom: OverviewZoom
): { start: number; end: number } {
	if (!(coverageEnd > coverageStart) || zoom === 'all') {
		return { start: coverageStart, end: coverageEnd };
	}
	const span = zoom === '24h' ? 24 * 60 * 60 * 1000 : 7 * 24 * 60 * 60 * 1000;
	return { start: Math.max(coverageStart, coverageEnd - span), end: coverageEnd };
}

export function zoomForWindow(ms: number): OverviewZoom {
	if (ms <= 6 * 60 * 60 * 1000) return '24h';
	if (ms <= 7 * 24 * 60 * 60 * 1000) return '7d';
	return 'all';
}

export function matchingPreset(latestWindowMs: number, followLatest: boolean): string {
	if (!followLatest) return '';
	if (latestWindowMs >= 30 * 24 * 60 * 60 * 1000) return 'All';
	const match = WINDOW_PRESETS.find((preset) => Math.abs(preset.ms - latestWindowMs) < 60_000);
	return match?.label ?? '';
}

export function binTraffic(
	buckets: TrafficPoint[],
	domainStart: number,
	domainEnd: number,
	binCount: number
): SparkBin[] {
	const count = Math.max(1, Math.floor(binCount));
	const span = domainEnd - domainStart;
	if (span <= 0) return [];
	const bins: SparkBin[] = Array.from({ length: count }, (_, index) => ({
		start: domainStart + (span * index) / count,
		end: domainStart + (span * (index + 1)) / count,
		bytes: 0
	}));
	for (const bucket of buckets) {
		if (bucket.time < domainStart || bucket.time >= domainEnd) continue;
		const index = Math.min(count - 1, Math.floor(((bucket.time - domainStart) / span) * count));
		bins[index].bytes += bucket.bytes;
	}
	return bins;
}

export function sparklineWindows(start: number, end: number): { start: number; end: number }[] {
	if (!(end > start)) return [];
	const span = end - start;
	if (span <= HOURLY_CHUNK_MS) return [{ start, end }];
	const count = Math.min(MAX_SPARK_REQUESTS, Math.ceil(span / HOURLY_CHUNK_MS));
	const size = span / count;
	const windows: { start: number; end: number }[] = [];
	for (let index = 0; index < count; index++) {
		const windowStart = start + index * size;
		const windowEnd = index === count - 1 ? end : start + (index + 1) * size;
		windows.push({ start: windowStart, end: windowEnd });
	}
	return windows;
}

function clamp(value: number, min: number, max: number): number {
	return Math.min(max, Math.max(min, value));
}

export function timeToRatio(time: number, start: number, end: number): number {
	if (end <= start) return 0;
	return clamp((time - start) / (end - start), 0, 1);
}

export function brushHit(
	ratio: number,
	startRatio: number,
	endRatio: number,
	edgeRatio: number
): BrushAction {
	const left = Math.min(startRatio, endRatio);
	const right = Math.max(startRatio, endRatio);
	const distStart = Math.abs(ratio - left);
	const distEnd = Math.abs(ratio - right);
	if (distStart <= edgeRatio && distStart <= distEnd) return 'start';
	if (distEnd <= edgeRatio) return 'end';
	if (ratio >= left && ratio <= right) return 'move';
	return 'create';
}

export function applyBrushDrag(input: {
	action: BrushAction;
	originRatio: number;
	ratio: number;
	originStart: number;
	originEnd: number;
	domainStart: number;
	domainEnd: number;
	minMs?: number;
}): { start: number; end: number } {
	const minMs = input.minMs ?? MIN_WINDOW_MS;
	const domain = Math.max(0, input.domainEnd - input.domainStart);
	const minWindow = Math.min(Math.max(minMs, 1), Math.max(domain, 1));
	const at = (ratio: number) => input.domainStart + clamp(ratio, 0, 1) * domain;

	if (input.action === 'move') {
		const width = Math.min(domain, Math.max(minWindow, input.originEnd - input.originStart));
		const delta = (clamp(input.ratio, 0, 1) - clamp(input.originRatio, 0, 1)) * domain;
		let start = input.originStart + delta;
		let end = start + width;
		if (start < input.domainStart) {
			start = input.domainStart;
			end = start + width;
		}
		if (end > input.domainEnd) {
			end = input.domainEnd;
			start = end - width;
		}
		return { start, end };
	}

	if (input.action === 'start') {
		let end = input.originEnd;
		let start = Math.min(at(input.ratio), end - minWindow);
		if (start < input.domainStart) {
			start = input.domainStart;
			end = Math.min(input.domainEnd, Math.max(end, start + minWindow));
		}
		return { start, end };
	}

	if (input.action === 'end') {
		let start = input.originStart;
		let end = Math.max(at(input.ratio), start + minWindow);
		if (end > input.domainEnd) {
			end = input.domainEnd;
			start = Math.max(input.domainStart, Math.min(start, end - minWindow));
		}
		return { start, end };
	}

	let start = at(Math.min(input.originRatio, input.ratio));
	let end = at(Math.max(input.originRatio, input.ratio));
	if (end - start < minWindow) {
		const mid = (start + end) / 2;
		start = mid - minWindow / 2;
		end = mid + minWindow / 2;
		if (start < input.domainStart) {
			start = input.domainStart;
			end = Math.min(input.domainEnd, start + minWindow);
		}
		if (end > input.domainEnd) {
			end = input.domainEnd;
			start = Math.max(input.domainStart, end - minWindow);
		}
	}
	return { start, end };
}

export function nudgeBrush(
	which: 'start' | 'end',
	key: string,
	shift: boolean,
	start: number,
	end: number,
	domainStart: number,
	domainEnd: number
): { start: number; end: number } | null {
	const small = shift ? 60 * 60 * 1000 : MIN_WINDOW_MS;
	const page = shift ? 6 * 60 * 60 * 1000 : 60 * 60 * 1000;
	let nextStart = start;
	let nextEnd = end;
	if (key === 'ArrowLeft' || key === 'ArrowDown') {
		if (which === 'start') nextStart -= small;
		else nextEnd -= small;
	} else if (key === 'ArrowRight' || key === 'ArrowUp') {
		if (which === 'start') nextStart += small;
		else nextEnd += small;
	} else if (key === 'PageDown') {
		if (which === 'start') nextStart -= page;
		else nextEnd -= page;
	} else if (key === 'PageUp') {
		if (which === 'start') nextStart += page;
		else nextEnd += page;
	} else if (key === 'Home') {
		if (which === 'start') nextStart = domainStart;
		else nextEnd = nextStart + MIN_WINDOW_MS;
	} else if (key === 'End') {
		if (which === 'end') nextEnd = domainEnd;
		else nextStart = nextEnd - MIN_WINDOW_MS;
	} else {
		return null;
	}
	return clampWindow(nextStart, nextEnd, domainStart, domainEnd, which);
}

export function clampWindow(
	startMs: number,
	endMs: number,
	earliest: number,
	latest: number,
	anchor: 'start' | 'end' | 'both' = 'both'
): { start: number; end: number } {
	const span = Math.max(MIN_WINDOW_MS, latest - earliest);
	let start = startMs;
	let end = endMs;
	let duration = Math.max(MIN_WINDOW_MS, end - start);
	duration = Math.min(duration, span);

	if (anchor === 'start') {
		start = clamp(start, earliest, latest - duration);
		end = start + duration;
	} else if (anchor === 'end') {
		end = clamp(end, earliest + duration, latest);
		start = end - duration;
	} else {
		if (end > latest) {
			end = latest;
			start = end - duration;
		}
		if (start < earliest) {
			start = earliest;
			end = start + duration;
		}
		if (end > latest) end = latest;
	}
	if (end <= start) end = Math.min(latest, start + MIN_WINDOW_MS);
	return { start, end };
}

export function validateWindow(
	start: Date | null,
	end: Date | null,
	earliest: number,
	latest: number
): string | null {
	if (!start || !end || Number.isNaN(start.getTime()) || Number.isNaN(end.getTime())) {
		return 'Enter a valid start and end.';
	}
	if (end.getTime() <= start.getTime()) return 'End must be after start.';
	if (end.getTime() - start.getTime() < MIN_WINDOW_MS) return 'Use at least 5 minutes.';
	if (start.getTime() < earliest - 60_000 || end.getTime() > latest + 60_000) {
		return 'That time is outside the data we have.';
	}
	return null;
}

export function formatWindow(ms: number): string {
	if (!Number.isFinite(ms) || ms <= 0) return '--';
	const minutes = Math.round(ms / 60_000);
	if (minutes < 60) return `${minutes}m`;
	const hours = minutes / 60;
	if (hours < 48) {
		const rounded = Math.round(hours * 10) / 10;
		return Number.isInteger(rounded) ? `${rounded}h` : `${rounded.toFixed(1)}h`;
	}
	const days = hours / 24;
	const rounded = Math.round(days * 10) / 10;
	return Number.isInteger(rounded) ? `${rounded}d` : `${rounded.toFixed(1)}d`;
}

export function formatStamp(date: Date | number | null): string {
	if (date == null) return '--';
	const value = typeof date === 'number' ? new Date(date) : date;
	if (Number.isNaN(value.getTime())) return '--';
	return value.toLocaleString(undefined, {
		month: 'short',
		day: 'numeric',
		hour: '2-digit',
		minute: '2-digit'
	});
}

export function windowSummary(
	start: number,
	end: number,
	coverageStart: number,
	coverageEnd: number
): string {
	return `${formatStamp(start)} – ${formatStamp(end)} · ${formatWindow(end - start)} · coverage ${formatStamp(coverageStart)} – ${formatStamp(coverageEnd)}`;
}

export function liveRefreshEvery(pollInterval: string | null | undefined): string {
	return formatWindow(refreshIntervalMs(pollInterval));
}
