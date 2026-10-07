import { DEFAULT_WINDOW_MS, MIN_WINDOW_MS, formatWindow, type TrafficPoint } from './time-window';
import { formatStamp, type RangeIntent, type TimeZoneMode } from './time-range-url';

export interface Span {
	start: number;
	end: number;
}

export type MissingReason = 'before-start' | 'after-end' | 'gap';

export type RangeCoverage =
	| { kind: 'full' }
	| { kind: 'partial'; coveredMs: number; totalMs: number; reason: MissingReason }
	| { kind: 'none'; reason: MissingReason | 'no-data' };

const HOUR_MS = 60 * 60 * 1000;
// Overview buckets are hourly, so edges within a few minutes are noise.
const EDGE_TOLERANCE_MS = 5 * 60 * 1000;

// Spans of time with traffic, merged from overview buckets. A bucket without
// bytes is a gap, the same as a missing bucket.
export function dataSpans(points: TrafficPoint[], fallbackMs = HOUR_MS): Span[] {
	const spans: Span[] = [];
	const ordered = points.filter((point) => point.bytes > 0).sort((a, b) => a.time - b.time);
	for (const point of ordered) {
		const end = point.time + (point.durationMs && point.durationMs > 0 ? point.durationMs : fallbackMs);
		const last = spans[spans.length - 1];
		if (last && point.time <= last.end) last.end = Math.max(last.end, end);
		else spans.push({ start: point.time, end });
	}
	return spans;
}

// How much of [start, end) has stored data. Without spans (the overview has not
// loaded or failed) only the stored range bounds are used.
export function rangeCoverage(
	start: number,
	end: number,
	coverage: Span | null,
	spans: Span[] | null
): RangeCoverage {
	if (!(end > start)) return { kind: 'full' };
	if (!coverage || !(coverage.end > coverage.start)) return { kind: 'none', reason: 'no-data' };
	const lo = Math.max(start, coverage.start);
	const hi = Math.min(end, coverage.end);
	if (hi <= lo) return { kind: 'none', reason: end <= coverage.start ? 'before-start' : 'after-end' };
	let covered = hi - lo;
	if (spans) {
		covered = 0;
		for (const span of spans) covered += Math.max(0, Math.min(hi, span.end) - Math.max(lo, span.start));
		if (covered <= 0) return { kind: 'none', reason: 'gap' };
	}
	const total = end - start;
	if (total - covered <= Math.max(EDGE_TOLERANCE_MS, total * 0.02)) return { kind: 'full' };
	const reason: MissingReason =
		start < coverage.start - EDGE_TOLERANCE_MS
			? 'before-start'
			: end > coverage.end + EDGE_TOLERANCE_MS
				? 'after-end'
				: 'gap';
	return { kind: 'partial', coveredMs: Math.min(covered, total), totalMs: total, reason };
}

// The instants an intent selects, given the stored range. Live windows end at
// the latest stored data, as the store resolves them.
export function intentRange(intent: RangeIntent, coverage: Span | null): Span | null {
	if (intent.kind === 'absolute') return { start: intent.start.getTime(), end: intent.end.getTime() };
	if (!coverage) return null;
	if (intent.kind === 'all') return { ...coverage };
	if (intent.kind === 'since') return { start: intent.start.getTime(), end: coverage.end };
	return { start: coverage.end - intent.windowMs, end: coverage.end };
}

export function storedRangeLabel(coverage: Span | null, zone: TimeZoneMode): string {
	if (!coverage) return '';
	return `${formatStamp(new Date(coverage.start), zone)} – ${formatStamp(new Date(coverage.end), zone)}`;
}

export interface CoverageNote {
	tone: 'warning' | 'muted';
	text: string;
	detail: string;
}

// Short text for the header bar and the picker. Full coverage says nothing.
export function coverageNote(result: RangeCoverage, coverage: Span | null, zone: TimeZoneMode): CoverageNote | null {
	if (result.kind === 'full') return null;
	const startsAt = coverage ? `Stored data starts ${formatStamp(new Date(coverage.start), zone)}.` : '';
	const endsAt = coverage ? `Stored data ends ${formatStamp(new Date(coverage.end), zone)}.` : '';
	if (result.kind === 'none') {
		if (result.reason === 'no-data') return { tone: 'warning', text: 'No stored data yet', detail: '' };
		const detail =
			result.reason === 'before-start'
				? `This range ends before stored data starts. ${startsAt}`
				: result.reason === 'after-end'
					? `This range starts after the latest stored data. ${endsAt}`
					: 'This range sits in a gap with no stored data.';
		return { tone: 'warning', text: 'No stored data in this range', detail };
	}
	const detail =
		result.reason === 'before-start' ? startsAt : result.reason === 'after-end' ? endsAt : 'Part of this range has no stored data.';
	return {
		tone: 'muted',
		text: `Data for ${formatWindow(result.coveredMs)} of ${formatWindow(result.totalMs)}`,
		detail
	};
}

// The line under "No data in this range".
export function emptyRangeReason(result: RangeCoverage): string {
	if (result.kind === 'none') {
		if (result.reason === 'no-data') return 'No flow data is stored yet.';
		if (result.reason === 'before-start') return 'This range ends before stored data starts.';
		if (result.reason === 'after-end') return 'This range starts after the latest stored data.';
		return 'This range sits in a gap with no stored data.';
	}
	return 'No flows were recorded in this range.';
}

// "Jump to latest data" keeps the window length, up to the whole stored range.
export function jumpWindowMs(selectedMs: number | null, coverage: Span | null): number {
	const wanted = selectedMs && selectedMs > 0 ? selectedMs : DEFAULT_WINDOW_MS;
	const stored = coverage ? coverage.end - coverage.start : wanted;
	return Math.max(MIN_WINDOW_MS, Math.min(wanted, stored));
}

// Jumping is pointless when nothing is stored, or when the window already
// ends at the latest data.
export function canJumpToLatest(result: RangeCoverage, live: boolean, coverage: Span | null): boolean {
	if (!coverage) return false;
	if (result.kind === 'none') return result.reason !== 'no-data';
	return !live;
}
