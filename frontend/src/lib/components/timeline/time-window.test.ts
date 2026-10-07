import { describe, expect, it } from 'vitest';
import {
	ALL_LIVE_MS,
	MIN_WINDOW_MS,
	applyBrushDrag,
	binTraffic,
	brushHit,
	continuousCoverageStart,
	matchingPreset,
	nudgeBrush,
	overviewDomain,
	resolveCoverage,
	sparklineWindows,
	validateWindow,
	zoomForWindow
} from './time-window';

const minute = 60_000;
const hour = 60 * minute;
const stray = Date.parse('2026-09-29T09:45:00.000Z');
const base = Date.parse('2026-10-05T00:00:00.000Z');

function series(start: number, count: number, step = minute): number[] {
	return Array.from({ length: count }, (_, index) => start + index * step);
}

describe('continuous coverage', () => {
	it('ignores a short early burst before a long gap', () => {
		const times = [stray, stray + minute, ...series(base, 180)];
		expect(continuousCoverageStart(times)).toBe(base);
	});

	it('keeps a gap that is part of the real series', () => {
		expect(continuousCoverageStart([...series(base, 30), ...series(base + 2 * hour, 30)])).toBe(base);
	});

	it('keeps an early run that is longer than a stray burst', () => {
		expect(continuousCoverageStart([...series(base, 180), ...series(base + 15 * hour, 60)])).toBe(base);
	});

	it('returns null when there are no buckets', () => {
		expect(continuousCoverageStart([])).toBeNull();
	});
});

describe('resolveCoverage', () => {
	it('trims the reported start when the series includes the stray burst', () => {
		const latest = base + 46 * hour;
		const coverage = resolveCoverage(stray, latest, [stray, stray + minute, base, base + hour]);
		expect(coverage.start).toBe(base);
		expect(coverage.end).toBe(latest);
	});

	it('leaves the reported start alone when this fetch does not reach it', () => {
		const latest = base + 46 * hour;
		const coverage = resolveCoverage(stray, latest, [latest - 24 * hour, latest - hour]);
		expect(coverage.start).toBe(stray);
	});
});

describe('traffic bins', () => {
	it('keeps quiet gaps empty and stacks bytes in the busy bins', () => {
		const start = base;
		const end = base + 4 * hour;
		const bins = binTraffic(
			[
				{ time: start + 10 * minute, bytes: 100 },
				{ time: start + 20 * minute, bytes: 50 },
				{ time: start + 3 * hour, bytes: 80 }
			],
			start,
			end,
			4
		);
		expect(bins).toHaveLength(4);
		expect(bins[0].bytes).toBe(150);
		expect(bins[1].bytes).toBeNull();
		expect(bins[2].bytes).toBeNull();
		expect(bins[3].bytes).toBe(80);
	});
});

describe('brush', () => {
	const domainStart = base;
	const domainEnd = base + 24 * hour;

	it('hits the nearer edge before the middle', () => {
		expect(brushHit(0.02, 0, 0.2, 0.04)).toBe('start');
		expect(brushHit(0.1, 0, 0.2, 0.04)).toBe('move');
		expect(brushHit(0.8, 0, 0.2, 0.04)).toBe('create');
	});

	it('creates a minimum window from a click', () => {
		const next = applyBrushDrag({
			action: 'create',
			originRatio: 0.5,
			ratio: 0.5,
			originStart: domainStart,
			originEnd: domainStart + 2 * hour,
			domainStart,
			domainEnd
		});
		expect(next.end - next.start).toBe(MIN_WINDOW_MS);
		expect(next.start).toBeGreaterThan(domainStart);
		expect(next.end).toBeLessThan(domainEnd);
	});

	it('moves the window without changing its length', () => {
		const next = applyBrushDrag({
			action: 'move',
			originRatio: 0.5,
			ratio: 0.25,
			originStart: domainEnd - 2 * hour,
			originEnd: domainEnd,
			domainStart,
			domainEnd
		});
		expect(next.end - next.start).toBe(2 * hour);
		expect(next.end).toBeLessThan(domainEnd);
	});

	it('nudges the focused edge and keeps the minimum length', () => {
		const next = nudgeBrush('start', 'ArrowRight', false, domainEnd - 2 * hour, domainEnd, domainStart, domainEnd);
		expect(next).not.toBeNull();
		expect(next!.start).toBe(domainEnd - 2 * hour + MIN_WINDOW_MS);
		expect(next!.end - next!.start).toBeGreaterThanOrEqual(MIN_WINDOW_MS);
	});
});

describe('overview and presets', () => {
	it('zooms the overview without moving the reported end', () => {
		const end = base + 10 * 24 * hour;
		expect(overviewDomain(base, end, '24h')).toEqual({ start: end - 24 * hour, end });
		expect(overviewDomain(base, end, 'all')).toEqual({ start: base, end });
	});

	it('uses a wider overview for longer live windows', () => {
		expect(zoomForWindow(2 * hour)).toBe('24h');
		expect(zoomForWindow(24 * hour)).toBe('7d');
		expect(zoomForWindow(ALL_LIVE_MS)).toBe('all');
	});

	it('highlights All only while that live mode is selected', () => {
		expect(matchingPreset(ALL_LIVE_MS, true)).toBe('All');
		expect(matchingPreset(2 * hour, true)).toBe('2h');
		expect(matchingPreset(2 * hour, false)).toBe('');
	});

	it('splits a week into hourly requests', () => {
		const windows = sparklineWindows(base, base + 7 * 24 * hour);
		expect(windows.length).toBeGreaterThan(1);
		expect(windows.length).toBeLessThanOrEqual(8);
		for (const window of windows) {
			expect(window.end - window.start).toBeLessThanOrEqual(48 * hour + 1);
		}
		expect(windows[0].start).toBe(base);
		expect(windows[windows.length - 1].end).toBe(base + 7 * 24 * hour);
	});
});

describe('validateWindow', () => {
	const earliest = base;
	const latest = base + 24 * hour;

	it('rejects an inverted or tiny window', () => {
		expect(validateWindow(new Date(latest), new Date(earliest), earliest, latest)).toMatch(/after start/);
		expect(validateWindow(new Date(earliest), new Date(earliest + minute), earliest, latest)).toMatch(/5 minutes/);
	});

	it('rejects a time outside coverage', () => {
		expect(validateWindow(new Date(stray), new Date(stray + 2 * hour), earliest, latest)).toMatch(/outside/);
	});

	it('accepts a window inside coverage', () => {
		expect(validateWindow(new Date(latest - 2 * hour), new Date(latest), earliest, latest)).toBeNull();
	});
});

describe('resolveCoverage start', () => {
	it('never starts before the first stored minute, even when its hourly bucket does', () => {
		const reported = Date.parse('2026-10-05T01:24:00.000Z');
		const firstBucket = Date.parse('2026-10-05T01:00:00.000Z');
		const end = Date.parse('2026-10-07T13:00:00.000Z');
		const buckets = Array.from({ length: 10 }, (_, index) => firstBucket + index * 60 * 60 * 1000);
		expect(resolveCoverage(reported, end, buckets).start).toBe(reported);
	});
});

