import { beforeEach, describe, expect, it, vi } from 'vitest';
import { get, writable } from 'svelte/store';

const calls = vi.hoisted(() => ({ list: [] as [number, number][] }));
const tailnet = vi.hoisted(() => ({ store: null as unknown }));

vi.mock('#lib/services', () => ({
	tailscaleService: {
		getBandwidth: async (start: Date, end: Date) => {
			calls.list.push([start.getTime(), end.getTime()]);
			return {
				buckets: [{ time: start.toISOString(), txBytes: 600, rxBytes: 0, seconds: 60 }],
				metadata: { bucketSeconds: 60 }
			};
		}
	}
}));
vi.mock('./tailnet-store', () => {
	tailnet.store = writable<string | null>('home');
	return { selectedTailnetId: tailnet.store };
});

import { loadTrafficDetail, resetTrafficDetailForTests, trafficDetail, DETAIL_CHUNK_MS } from './traffic-detail';

const hour = 60 * 60 * 1000;
const base = Date.parse('2026-10-05T00:00:00Z');
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

describe('minute detail', () => {
	beforeEach(() => {
		resetTrafficDetailForTests();
		calls.list = [];
		(tailnet.store as ReturnType<typeof writable<string | null>>).set('home');
	});

	it('fetches two hour slices and refetches only the newest when data moves on', async () => {
		loadTrafficDetail(base, base + 5 * hour, base + 5 * hour);
		await settle();
		expect(calls.list).toHaveLength(3);
		expect(calls.list.every(([start, end]) => end - start <= DETAIL_CHUNK_MS)).toBe(true);
		expect(get(trafficDetail)?.points).toHaveLength(3);

		calls.list = [];
		loadTrafficDetail(base + 60_000, base + 5 * hour + 60_000, base + 5 * hour + 60_000);
		await settle();
		expect(calls.list).toEqual([[base + 4 * hour, base + 5 * hour + 60_000]]);
	});

	it('does not refetch an unchanged view', async () => {
		loadTrafficDetail(base, base + 4 * hour, base + 4 * hour);
		await settle();
		calls.list = [];
		loadTrafficDetail(base, base + 4 * hour, base + 4 * hour);
		await settle();
		expect(calls.list).toHaveLength(0);
	});

	it('drops the cache when the tailnet changes', async () => {
		loadTrafficDetail(base, base + 4 * hour, base + 4 * hour);
		await settle();
		calls.list = [];
		(tailnet.store as ReturnType<typeof writable<string | null>>).set('lab');
		loadTrafficDetail(base, base + 4 * hour, base + 4 * hour);
		await settle();
		expect(calls.list).toHaveLength(2);
	});

	it('leaves long spans to the hourly overview', async () => {
		loadTrafficDetail(base, base + 72 * hour, base + 72 * hour);
		await settle();
		expect(calls.list).toHaveLength(0);
		expect(get(trafficDetail)).toBeNull();
	});
});
