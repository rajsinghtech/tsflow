import { beforeEach, describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import { dataSourceStore } from '#lib/stores/data-source-store';
import type { TailnetInfo } from '#lib/services/tailnet-query';
import {
	loadRankedTalkers,
	loadRankings,
	rankSort,
	rankedPairs,
	rankedTalkers,
	resetRankingsForTests,
	setRankSort
} from '#lib/stores/rankings-store';
import {
	resetTailnetStateForTests,
	selectTailnet,
	setTailnetPathReader,
	setTailnetSearchReader
} from '#lib/stores/tailnet-store';

const calls: string[] = [];
let tailnetList: TailnetInfo[] = [];
let failTalkers = false;
let emptyTalkers = false;

const start = new Date('2026-03-01T12:00:00.000Z');
const end = new Date('2026-03-01T14:00:00.000Z');
const startISO = start.toISOString();
const endISO = end.toISOString();

function tailnet(id: string, displayName: string): TailnetInfo {
	return { id, displayName, poller: { running: true, lastError: '', pollErrors: 0 } };
}

function json(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

function metadata(url: string, count: number, hasMore: boolean) {
	const params = new URL(url, 'http://local').searchParams;
	return {
		start: startISO,
		end: endISO,
		tailnet: params.get('tailnet') ?? 'default',
		limit: 20,
		offset: Number(params.get('offset') ?? '0'),
		count,
		hasMore,
		sort: params.get('sort') ?? 'bytes'
	};
}

beforeEach(() => {
	calls.length = 0;
	failTalkers = false;
	emptyTalkers = false;
	tailnetList = [tailnet('default', 'example.com')];
	resetRankingsForTests();
	resetTailnetStateForTests();
	dataSourceStore.reset();
	dataSourceStore.setSelectedRange(start, end);

	globalThis.fetch = (async (input: RequestInfo | URL) => {
		const url = String(input);
		calls.push(url);
		if (url.includes('/api/tailnets')) return json({ tailnets: tailnetList });
		if (url.includes('/flow-logs/range')) return json({ earliest: '', latest: '', count: 0 });
		if (url.includes('/analytics/talkers')) {
			if (failTalkers) return new Response('no', { status: 500 });
			if (emptyTalkers) return json({ talkers: [], metadata: metadata(url, 0, false) });
			const offset = Number(new URL(url, 'http://local').searchParams.get('offset') ?? '0');
			return json({
				talkers: [
					{
						nodeId: offset === 0 ? 'a' : 'b',
						hostname: offset === 0 ? 'laptop' : 'phone',
						txBytes: 30,
						rxBytes: 10,
						totalBytes: 40,
						flowCount: 9
					}
				],
				metadata: metadata(url, 1, offset === 0)
			});
		}
		if (url.includes('/analytics/pairs')) {
			return json({
				pairs: [
					{
						srcNodeId: 'a',
						srcHostname: 'laptop',
						dstNodeId: 'b',
						dstHostname: 'phone',
						txBytes: 30,
						rxBytes: 5,
						totalBytes: 35,
						flowCount: 2
					}
				],
				metadata: metadata(url, 1, false)
			});
		}
		if (url.includes('/devices')) return json({ devices: [] });
		if (url.includes('/stats/')) return json({ talkers: [], pairs: [], summary: null, buckets: [] });
		return json({});
	}) as typeof fetch;
});

describe('ranked analytics requests', () => {
	it('loads both tables for a single tailnet without a tailnet parameter', async () => {
		await loadRankings();

		expect(calls[0]).toBe('/api/tailnets');
		expect(calls).toContain(
			`/api/analytics/talkers?start=${startISO}&end=${endISO}&limit=20&offset=0&sort=bytes`
		);
		expect(calls).toContain(
			`/api/analytics/pairs?start=${startISO}&end=${endISO}&limit=20&offset=0&sort=bytes`
		);
		expect(calls.some((url) => url.includes('tailnet='))).toBe(false);
		expect(get(rankedTalkers).rows[0]?.hostname).toBe('laptop');
		expect(get(rankedTalkers).hasMore).toBe(true);
		expect(get(rankedPairs).rows[0]?.dstHostname).toBe('phone');
		expect(calls.some((url) => url.includes('/api/stats/'))).toBe(false);
	});

	it('requests the next talker page with offset', async () => {
		await loadRankedTalkers(20);
		expect(calls.some((url) => url.includes('/analytics/talkers') && url.includes('offset=20'))).toBe(true);
		expect(get(rankedTalkers).offset).toBe(20);
		expect(get(rankedTalkers).hasMore).toBe(false);
		expect(get(rankedTalkers).rows[0]?.hostname).toBe('phone');
	});

	it('sorts both tables by flows and returns to the first page', async () => {
		await loadRankedTalkers(20);
		calls.length = 0;
		await setRankSort('flows');

		const ranked = calls.filter((url) => url.includes('/api/analytics/'));
		expect(ranked).toHaveLength(2);
		expect(ranked.every((url) => url.includes('sort=flows') && url.includes('offset=0'))).toBe(true);
		expect(get(rankSort)).toBe('flows');
		expect(get(rankedTalkers).offset).toBe(0);
		expect(get(rankedPairs).offset).toBe(0);
	});

	it('does not refetch when the sort is already selected', async () => {
		await loadRankings();
		calls.length = 0;
		await setRankSort('bytes');
		expect(calls).toEqual([]);
	});

	it('sends the selected tailnet when several are configured', async () => {
		tailnetList = [tailnet('default', 'example.com'), tailnet('lab', 'lab.example.com')];
		setTailnetSearchReader(() => '?tailnet=lab');
		await loadRankedTalkers(0);
		const talkers = calls.find((url) => url.includes('/analytics/talkers'));
		expect(talkers).toContain('tailnet=lab');
		expect(talkers).toContain(`start=${startISO}`);
	});

	it('reloads rankings for the new tailnet and leaves stats and the graph alone', async () => {
		tailnetList = [tailnet('default', 'example.com'), tailnet('lab', 'lab.example.com')];
		setTailnetSearchReader(() => '?tailnet=default');
		await loadRankedTalkers(0);
		calls.length = 0;
		setTailnetPathReader(() => '/rankings');

		await selectTailnet('lab');

		const ranked = calls.filter((url) => url.includes('/api/analytics/'));
		expect(ranked.length).toBeGreaterThanOrEqual(2);
		expect(ranked.every((url) => url.includes('tailnet=lab'))).toBe(true);
		expect(calls.some((url) => url.includes('/api/devices'))).toBe(false);
		expect(calls.some((url) => url.includes('/api/stats/'))).toBe(false);
	});

	it('keeps an empty page and records a talker error without dropping pairs', async () => {
		emptyTalkers = true;
		failTalkers = false;
		await loadRankings();
		expect(get(rankedTalkers).rows).toEqual([]);
		expect(get(rankedTalkers).hasMore).toBe(false);
		expect(get(rankedTalkers).count).toBe(0);

		failTalkers = true;
		await loadRankings();
		expect(get(rankedTalkers).error).toMatch(/HTTP 500/);
		expect(get(rankedPairs).error).toBeNull();
		expect(get(rankedPairs).rows).toHaveLength(1);
	});
});
