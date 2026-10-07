import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

const calls = vi.hoisted(() => ({ network: 0, stats: 0, rankings: 0 }));

vi.mock('./network-store', () => ({
	loadNetworkData: vi.fn(async () => {
		calls.network++;
	})
}));
vi.mock('./stats-store', () => ({
	loadStats: vi.fn(async () => {
		calls.stats++;
	})
}));
vi.mock('./rankings-store', () => ({
	loadRankings: vi.fn(async () => {
		calls.rankings++;
	})
}));

import { pageRefresh, refreshVisibleData } from './live-mode';

function visit(pathname: string) {
	vi.stubGlobal('window', { location: { pathname } });
}

describe('refreshVisibleData', () => {
	beforeEach(() => {
		calls.network = 0;
		calls.stats = 0;
		calls.rankings = 0;
		pageRefresh.set(0);
	});
	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it('reloads the graph on Traffic', async () => {
		visit('/');
		await refreshVisibleData();
		expect(calls).toEqual({ network: 1, stats: 0, rankings: 0 });
		expect(get(pageRefresh)).toBe(0);
	});

	it('reloads stats and rankings on Analytics', async () => {
		visit('/analytics');
		await refreshVisibleData();
		expect(calls).toEqual({ network: 0, stats: 1, rankings: 1 });
	});

	it('leaves Policy alone', async () => {
		visit('/policy');
		await refreshVisibleData();
		expect(calls).toEqual({ network: 0, stats: 0, rankings: 0 });
		expect(get(pageRefresh)).toBe(0);
	});

	it('asks pages with their own data to reload instead of loading the graph', async () => {
		visit('/new');
		await refreshVisibleData();
		expect(calls).toEqual({ network: 0, stats: 0, rankings: 0 });
		expect(get(pageRefresh)).toBe(1);
	});
});
