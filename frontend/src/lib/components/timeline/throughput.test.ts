import { describe, expect, it } from 'vitest';
import { byteBins, formatBps, niceCeiling, peakBin, ratePaths, rateBins, slotLabel, windowRates } from './throughput';
import { detailSlices, DETAIL_CHUNK_MS } from '#lib/stores/traffic-detail';

const minute = 60_000;
const hour = 60 * minute;
const base = Date.parse('2026-10-05T00:00:00.000Z');

describe('rate bins', () => {
	it('turns bytes per bucket into bits per second', () => {
		// 450 GB in an hour is 1 Gbps.
		const bins = rateBins([{ time: base, bytes: 450e9, durationMs: hour }], base, base + hour, 4);
		expect(bins.map((bin) => Math.round(bin.bps ?? -1))).toEqual([1e9, 1e9, 1e9, 1e9]);
	});

	it('spreads an hourly bucket over narrow bins instead of repeating its bytes', () => {
		const bins = rateBins([{ time: base, bytes: 3600 * 125, durationMs: hour }], base, base + hour, 60);
		expect(new Set(bins.map((bin) => Math.round(bin.bps ?? 0)))).toEqual(new Set([1000]));
	});

	it('averages minute buckets that fall in one wide bin', () => {
		const points = [
			{ time: base, bytes: 60 * 125, durationMs: minute },
			{ time: base + minute, bytes: 3 * 60 * 125, durationMs: minute }
		];
		expect(rateBins(points, base, base + 2 * minute, 1)[0].bps).toBeCloseTo(2000);
	});

	it('uses only the covered part of a partial bucket', () => {
		// The current hour has 15 minutes of data.
		const point = { time: base, bytes: 15 * 60 * 125, durationMs: hour, coveredMs: 15 * minute };
		const bins = rateBins([point], base, base + hour, 4);
		expect(bins[0].bps).toBeCloseTo(1000);
		expect(bins.slice(1).map((bin) => bin.bps)).toEqual([null, null, null]);
	});

	it('keeps gaps as null, not zero', () => {
		const points = [
			{ time: base, bytes: 1000, durationMs: minute },
			{ time: base + 3 * minute, bytes: 1000, durationMs: minute }
		];
		expect(rateBins(points, base, base + 4 * minute, 4).map((bin) => bin.bps === null)).toEqual([false, true, true, false]);
	});
});

describe('window rates', () => {
	it('reports the newest and the highest bucket in the window', () => {
		const points = [
			{ time: base, bytes: 60 * 125, durationMs: minute },
			{ time: base + minute, bytes: 600 * 125, durationMs: minute },
			{ time: base + 2 * minute, bytes: 120 * 125, durationMs: minute },
			{ time: base + 10 * minute, bytes: 6000 * 125, durationMs: minute }
		];
		const rates = windowRates(points, base, base + 3 * minute);
		expect(rates.latest).toBeCloseTo(2000);
		expect(rates.peak).toBeCloseTo(10000);
	});

	it('is empty without data', () => {
		expect(windowRates([], base, base + hour)).toEqual({ latest: null, peak: null, latestAt: null });
	});
});

describe('axis and labels', () => {
	it('rounds the axis top up', () => {
		expect(niceCeiling(0.94e9)).toBe(1e9);
		expect(niceCeiling(1.3e9)).toBe(1.5e9);
		expect(niceCeiling(100.4e6)).toBe(120e6);
		expect(niceCeiling(2.2e6)).toBe(2.5e6);
		expect(niceCeiling(4.5e3)).toBe(5e3);
		expect(niceCeiling(0)).toBe(1);
	});

	it('scales the unit with the value', () => {
		expect(formatBps(940e6)).toBe('940 Mbps');
		expect(formatBps(1.234e9)).toBe('1.23 Gbps');
		expect(formatBps(12.5e3)).toBe('12.5 Kbps');
		expect(formatBps(800)).toBe('800 bps');
		expect(formatBps(0)).toBe('0 bps');
		expect(formatBps(null)).toBe('0 bps');
	});

	it('breaks the line at gaps', () => {
		const paths = ratePaths(
			[
				{ start: 0, end: 1, bps: 10, coveredMs: 1 },
				{ start: 1, end: 2, bps: null, coveredMs: 0 },
				{ start: 2, end: 3, bps: 5, coveredMs: 1 }
			],
			10,
			30,
			38
		);
		expect(paths.line.match(/M/g)).toHaveLength(2);
		expect(paths.area.match(/Z/g)).toHaveLength(2);
	});
});

describe('minute detail slices', () => {
	it('uses fixed two hour slices so finished ones can be cached', () => {
		const start = base + 30 * minute;
		const slices = detailSlices(start, base + 5 * hour, base + 5 * hour - 10 * minute);
		expect(slices[0]).toEqual({ start: base, end: base + DETAIL_CHUNK_MS });
		expect(slices.at(-1)).toEqual({ start: base + 4 * hour, end: base + 5 * hour - 10 * minute });
		expect(slices.every((slice) => slice.end - slice.start <= DETAIL_CHUNK_MS)).toBe(true);
	});
});

describe('bytes view', () => {
	it('counts each bar its share of an hourly bucket', () => {
		const bins = byteBins(rateBins([{ time: base, bytes: 3600, durationMs: hour }], base, base + hour, 4));
		expect(bins.map((bin) => Math.round(bin.bytes ?? -1))).toEqual([900, 900, 900, 900]);
	});

	it('labels the bar width', () => {
		expect(slotLabel(16 * minute)).toBe('16 min');
		expect(slotLabel(2 * hour)).toBe('2 h');
		expect(slotLabel(3 * 24 * hour)).toBe('3 d');
	});
});

describe('strip peak', () => {
	it('picks the highest bin and skips gaps', () => {
		expect(
			peakBin([
				{ start: 0, end: 1, bps: 5, coveredMs: 1 },
				{ start: 1, end: 2, bps: null, coveredMs: 0 },
				{ start: 2, end: 3, bps: 9, coveredMs: 1 },
				{ start: 3, end: 4, bps: 2, coveredMs: 1 }
			])
		).toEqual({ index: 2, bps: 9 });
		expect(peakBin([])).toBeNull();
	});
});
