import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import { dataSourceStore } from './data-source-store';
import { createLiveRefresh } from './live-refresh';
import { resetTailnetQueryForTests } from '#lib/services/tailnet-query';

function json(body: unknown): Response {
	return new Response(JSON.stringify(body), {
		status: 200,
		headers: { 'Content-Type': 'application/json' }
	});
}

const range = {
	earliest: '2026-10-05T00:00:00.000Z',
	latest: '2026-10-06T22:19:00.000Z',
	count: 40
};

describe('createLiveRefresh', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		resetTailnetQueryForTests();
		dataSourceStore.reset();
		vi.stubGlobal('fetch', async (input: RequestInfo | URL) => {
			const url = String(input);
			if (url.includes('/api/tailnets')) return json({ tailnets: [] });
			if (url.includes('/poller/status')) {
				return json({
					running: true,
					lastPollTime: range.latest,
					lastPollCount: 1,
					totalPolled: 1,
					pollErrors: 0,
					pollInterval: '5m0s',
					database: { dbSizeBytes: 1, dataRange: { earliest: '', latest: '', count: 0 } }
				});
			}
			return json({});
		});
	});

	afterEach(() => {
		vi.unstubAllGlobals();
		vi.useRealTimers();
		dataSourceStore.reset();
	});

	it('reloads on the poll cadence only while the window is live', async () => {
		const calls: number[] = [];
		const live = createLiveRefresh(() => {
			calls.push(Date.now());
		});
		await dataSourceStore.fetchPollerStatus();
		dataSourceStore.showLatestWindow(range, 2 * 60 * 60 * 1000);
		live.start(60_000);

		vi.advanceTimersByTime(5 * 60 * 1000);
		expect(calls).toHaveLength(1);

		const state = get(dataSourceStore);
		dataSourceStore.setSelectedRange(state.selectedStart!, state.selectedEnd!);
		vi.advanceTimersByTime(10 * 60 * 1000);
		expect(calls).toHaveLength(1);

		dataSourceStore.showLatestWindow(range, 2 * 60 * 60 * 1000);
		vi.advanceTimersByTime(5 * 60 * 1000);
		expect(calls).toHaveLength(2);
		expect(get(dataSourceStore).followLatest).toBe(true);

		live.dispose();
	});
});
