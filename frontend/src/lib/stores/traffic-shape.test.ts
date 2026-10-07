import { beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

const H = 60 * 60 * 1000;
const t0 = Date.parse('2026-10-05T00:00:00.000Z');
const calls = vi.hoisted(() => ({ bandwidth: 0 }));

// Synthetic overview: traffic for hours 0-24 and 40-60, nothing between.
vi.mock('#lib/services', () => ({
	tailscaleService: {
		getDataRange: vi.fn(async () => ({
			earliest: '2026-10-05T00:00:00.000Z',
			latest: '2026-10-07T12:00:00.000Z',
			count: 100
		})),
		getBandwidth: vi.fn(async (start: Date, end: Date) => {
			calls.bandwidth++;
			const buckets = [];
			for (let t = Math.ceil(start.getTime() / H) * H; t < end.getTime(); t += H) {
				const hour = (t - t0) / H;
				const bytes = hour < 24 || hour >= 40 ? 1000 : 0;
				buckets.push({ time: new Date(t).toISOString(), txBytes: bytes, rxBytes: 0 });
			}
			return { buckets, metadata: { bucketSeconds: 3600 } };
		})
	}
}));

import { dataSourceStore } from './data-source-store';
import { loadTrafficShape, resetTrafficShapeForTests, storedSpans, trafficPoints, windowCoverage } from './traffic-shape';

const earliest = new Date(t0).toISOString();
const latest = new Date(t0 + 60 * H).toISOString();

async function settle() {
	for (let i = 0; i < 5; i++) await Promise.resolve();
	await new Promise((resolve) => setTimeout(resolve, 0));
}

describe('traffic shape', () => {
	beforeEach(() => {
		calls.bandwidth = 0;
		resetTrafficShapeForTests();
		dataSourceStore.reset();
	});

	it('loads the overview once per stored range', async () => {
		loadTrafficShape(earliest, latest);
		await settle();
		const first = calls.bandwidth;
		expect(first).toBeGreaterThan(0);
		expect(get(trafficPoints)?.length).toBe(60);

		// Another page mounting the controls asks again with the same range.
		loadTrafficShape(earliest, latest);
		await settle();
		expect(calls.bandwidth).toBe(first);

		// A new poll moves the latest stored minute.
		loadTrafficShape(earliest, new Date(t0 + 61 * H).toISOString());
		await settle();
		expect(calls.bandwidth).toBeGreaterThan(first);
	});

	it('tells which part of the selected window has stored data', async () => {
		await dataSourceStore.fetchDataRange();
		loadTrafficShape(earliest, latest);
		await settle();
		expect(get(storedSpans)).toEqual([
			{ start: t0, end: t0 + 24 * H },
			{ start: t0 + 40 * H, end: t0 + 60 * H }
		]);

		dataSourceStore.setSelectedRange(new Date(t0 + 28 * H), new Date(t0 + 35 * H));
		expect(get(windowCoverage)).toEqual({ kind: 'none', reason: 'gap' });

		dataSourceStore.setSelectedRange(new Date(t0 - 10 * H), new Date(t0 - 4 * H));
		expect(get(windowCoverage)).toEqual({ kind: 'none', reason: 'before-start' });

		dataSourceStore.setSelectedRange(new Date(t0 + 37 * H), new Date(t0 + 43 * H));
		expect(get(windowCoverage)).toMatchObject({ kind: 'partial', reason: 'gap', coveredMs: 3 * H, totalMs: 6 * H });
	});
});
