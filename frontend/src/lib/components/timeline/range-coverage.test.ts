import { describe, expect, it } from 'vitest';
import { canJumpToLatest, coverageNote, dataSpans, emptyRangeReason, intentRange, jumpWindowMs, rangeCoverage, storedRangeLabel } from './range-coverage';

const H = 60 * 60 * 1000;
const t0 = Date.parse('2026-10-05T00:00:00.000Z');
const at = (hours: number) => t0 + hours * H;
// Stored data: hours 0-24, a gap 24-40, then 40-60.
const coverage = { start: at(0), end: at(60) };
const points = [
	...Array.from({ length: 24 }, (_, i) => ({ time: at(i), bytes: 1000, durationMs: H })),
	...Array.from({ length: 16 }, (_, i) => ({ time: at(24 + i), bytes: 0, durationMs: H })),
	...Array.from({ length: 20 }, (_, i) => ({ time: at(40 + i), bytes: 1000, durationMs: H }))
];
const spans = dataSpans(points);

describe('dataSpans', () => {
	it('merges buckets with bytes and treats empty buckets as gaps', () => {
		expect(spans).toEqual([
			{ start: at(0), end: at(24) },
			{ start: at(40), end: at(60) }
		]);
	});
});

describe('rangeCoverage', () => {
	it('finds nothing before stored data starts', () => {
		expect(rangeCoverage(at(-10), at(-4), coverage, spans)).toEqual({ kind: 'none', reason: 'before-start' });
	});

	it('finds nothing inside a gap', () => {
		expect(rangeCoverage(at(28), at(35), coverage, spans)).toEqual({ kind: 'none', reason: 'gap' });
	});

	it('finds nothing after the latest stored data', () => {
		expect(rangeCoverage(at(61), at(65), coverage, spans)).toEqual({ kind: 'none', reason: 'after-end' });
	});

	it('reports a range that overlaps the data start as partial', () => {
		expect(rangeCoverage(at(-3), at(3), coverage, spans)).toEqual({
			kind: 'partial',
			coveredMs: 3 * H,
			totalMs: 6 * H,
			reason: 'before-start'
		});
	});

	it('reports a range that overlaps a gap as partial', () => {
		expect(rangeCoverage(at(37), at(43), coverage, spans)).toEqual({
			kind: 'partial',
			coveredMs: 3 * H,
			totalMs: 6 * H,
			reason: 'gap'
		});
	});

	it('treats a range inside the data as full, ignoring a few minutes at the edges', () => {
		expect(rangeCoverage(at(2), at(10), coverage, spans)).toEqual({ kind: 'full' });
		expect(rangeCoverage(at(0) - 3 * 60_000, at(4), coverage, spans)).toEqual({ kind: 'full' });
	});

	it('falls back to the stored range bounds before the overview loads', () => {
		expect(rangeCoverage(at(28), at(35), coverage, null)).toEqual({ kind: 'full' });
		expect(rangeCoverage(at(-10), at(-4), coverage, null)).toEqual({ kind: 'none', reason: 'before-start' });
	});

	it('says no data when nothing is stored', () => {
		expect(rangeCoverage(at(0), at(1), null, null)).toEqual({ kind: 'none', reason: 'no-data' });
	});
});

describe('intentRange', () => {
	it('resolves live windows against the latest stored data', () => {
		expect(intentRange({ kind: 'sliding', windowMs: 2 * H }, coverage)).toEqual({ start: at(58), end: at(60) });
		expect(intentRange({ kind: 'since', start: new Date(at(50)) }, coverage)).toEqual({ start: at(50), end: at(60) });
		expect(intentRange({ kind: 'all' }, coverage)).toEqual(coverage);
	});

	it('keeps a pinned range as typed', () => {
		const intent = { kind: 'absolute' as const, start: new Date(at(-10)), end: new Date(at(-4)) };
		expect(intentRange(intent, null)).toEqual({ start: at(-10), end: at(-4) });
	});
});

describe('coverageNote', () => {
	it('warns when a range has no stored data, and says why', () => {
		expect(coverageNote({ kind: 'none', reason: 'before-start' }, coverage, 'utc')).toEqual({
			tone: 'warning',
			text: 'No stored data in this range',
			detail: 'This range ends before stored data starts. Stored data starts Oct 5, 12:00 AM.'
		});
		expect(coverageNote({ kind: 'none', reason: 'gap' }, coverage, 'utc')?.detail).toBe('This range sits in a gap with no stored data.');
	});

	it('keeps a partial range with a quiet note', () => {
		expect(coverageNote({ kind: 'partial', coveredMs: 3 * H, totalMs: 6 * H, reason: 'before-start' }, coverage, 'utc')).toEqual({
			tone: 'muted',
			text: 'Data for 3h of 6h',
			detail: 'Stored data starts Oct 5, 12:00 AM.'
		});
	});

	it('says nothing for full coverage', () => {
		expect(coverageNote({ kind: 'full' }, coverage, 'utc')).toBeNull();
	});

	it('labels the stored range', () => {
		expect(storedRangeLabel(coverage, 'utc')).toBe('Oct 5, 12:00 AM – Oct 7, 12:00 PM');
	});
});

describe('empty range', () => {
	it('explains why a range is empty', () => {
		expect(emptyRangeReason({ kind: 'none', reason: 'before-start' })).toBe('This range ends before stored data starts.');
		expect(emptyRangeReason({ kind: 'none', reason: 'gap' })).toBe('This range sits in a gap with no stored data.');
		expect(emptyRangeReason({ kind: 'full' })).toBe('No flows were recorded in this range.');
	});

	it('jumps with the same window length, no longer than the stored range', () => {
		expect(jumpWindowMs(6 * H, coverage)).toBe(6 * H);
		expect(jumpWindowMs(30 * 24 * H, coverage)).toBe(60 * H);
		expect(jumpWindowMs(null, coverage)).toBe(2 * H);
	});

	it('offers the jump unless nothing is stored or the window is already at the latest data', () => {
		expect(canJumpToLatest({ kind: 'none', reason: 'gap' }, false, coverage)).toBe(true);
		expect(canJumpToLatest({ kind: 'none', reason: 'no-data' }, false, null)).toBe(false);
		expect(canJumpToLatest({ kind: 'full' }, true, coverage)).toBe(false);
		expect(canJumpToLatest({ kind: 'full' }, false, coverage)).toBe(true);
	});
});
