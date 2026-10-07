import '#lib/stores/ui-store';
import { beforeEach, describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import type { Device, NetworkLog } from '#lib/types';
import { tailscaleService } from '#lib/services/tailscale-service';
import { fetchAndRenderPolicy, policyGraph } from '#lib/stores/policy-store';
import { devices, networkLogs } from '#lib/stores/network-store';
import { statsSummary } from '#lib/stores/stats-store';
import {
	chooseTailnet,
	hrefWithTailnet,
	searchWithTailnet,
	type TailnetInfo
} from '#lib/services/tailnet-query';
import {
	resetTailnetCaches,
	resetTailnetStateForTests,
	selectTailnet,
	selectedTailnetId,
	setTailnetPathReader,
	setTailnetSearchReader
} from '#lib/stores/tailnet-store';

const calls: string[] = [];
let tailnetStatus = 200;
let tailnetList: TailnetInfo[] = [];

function tailnet(id: string, displayName: string, lastError = ''): TailnetInfo {
	return {
		id,
		displayName,
		poller: { running: lastError === '', lastError, pollErrors: lastError ? 1 : 0 }
	};
}

function json(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

beforeEach(() => {
	calls.length = 0;
	tailnetStatus = 200;
	tailnetList = [tailnet('default', 'example.com')];
	resetTailnetStateForTests();
	globalThis.fetch = (async (input: RequestInfo | URL) => {
		const url = String(input);
		calls.push(url);
		if (url.includes('/api/tailnets')) {
			if (tailnetStatus !== 200) return new Response('missing', { status: tailnetStatus });
			return json({ tailnets: tailnetList });
		}
		if (url.includes('/flow-logs/range')) return json({ earliest: '', latest: '', count: 0 });
		if (url.includes('/devices')) return json({ devices: [] });
		if (url.includes('/services-records')) return json({ services: {}, records: {} });
		if (url.includes('/flow-logs/aggregated')) {
			return json({ flows: [], metadata: { count: 0, start: '', end: '', source: 'database' } });
		}
		if (url.includes('/poller/status')) {
			return json({
				running: true,
				lastPollTime: '',
				lastPollCount: 0,
				totalPolled: 0,
				pollErrors: 0,
				pollInterval: '1h'
			});
		}
		if (url.includes('/bandwidth')) {
			return json({ buckets: [], metadata: { count: 0, start: '', end: '', bucketSeconds: 60 } });
		}
		if (url.includes('/stats/')) {
			return json({
				summary: {
					tcpBytes: 1,
					udpBytes: 0,
					otherProtoBytes: 0,
					virtualBytes: 1,
					exitBytes: 0,
					subnetBytes: 0,
					physicalBytes: 0,
					totalFlows: 1,
					uniquePairs: 1
				},
				buckets: [],
				talkers: [],
				pairs: [],
				metadata: {}
			});
		}
		if (url.includes('/policy')) return json({});
		if (url.includes('/users')) return json({ users: [] });
		return json({});
	}) as typeof fetch;
});

const start = new Date('2026-03-01T12:00:00.000Z');
const end = new Date('2026-03-01T12:30:00.000Z');
const startISO = start.toISOString();
const endISO = end.toISOString();

describe('single tailnet requests', () => {
	it('issues the same data urls as a single-tailnet install after one list call', async () => {
		await tailscaleService.getDevices();
		await tailscaleService.getServicesRecords();
		await tailscaleService.getNetworkLogs(start, end);
		await tailscaleService.getAggregatedFlows(start, end);
		await tailscaleService.getDataRange();
		await tailscaleService.getPollerStatus();
		await tailscaleService.getBandwidth(start, end);
		await tailscaleService.getStatsOverview(start, end);
		await tailscaleService.getRankedTalkers({ start, end });
		await tailscaleService.getRankedPairs({ start, end, trafficTypes: ['virtual', 'subnet'] });
		await tailscaleService.getNodeStats('node-a', start, end);

		expect(calls[0]).toBe('/api/tailnets');
		expect(calls.filter((url) => url.includes('/api/tailnets'))).toHaveLength(1);
		expect(calls.slice(1)).toEqual([
			'/api/devices',
			'/api/services-records',
			`/api/network-logs?start=${startISO}&end=${endISO}`,
			`/api/flow-logs/aggregated?start=${startISO}&end=${endISO}`,
			'/api/flow-logs/range',
			'/api/poller/status',
			`/api/bandwidth?start=${startISO}&end=${endISO}`,
			`/api/stats/overview?start=${startISO}&end=${endISO}`,
			`/api/analytics/talkers?start=${startISO}&end=${endISO}&limit=20&offset=0&sort=bytes`,
			`/api/analytics/pairs?start=${startISO}&end=${endISO}&limit=20&offset=0&sort=bytes&trafficTypes=virtual,subnet`,
			`/api/stats/node/node-a?start=${startISO}&end=${endISO}`
		]);
		expect(calls.some((url) => url.includes('tailnet='))).toBe(false);
	});

	it('keeps policy urls unchanged', async () => {
		await fetchAndRenderPolicy();
		expect(calls).toEqual(['/api/tailnets', '/api/policy', '/api/users']);
	});

	it('ignores a tailnet query when only one tailnet is configured', async () => {
		setTailnetSearchReader(() => '?tailnet=lab');
		await tailscaleService.getDevices();
		expect(calls).toEqual(['/api/tailnets', '/api/devices']);
		expect(get(selectedTailnetId)).toBeNull();
	});

	it('treats a missing tailnet endpoint as single-tailnet', async () => {
		tailnetStatus = 404;
		await tailscaleService.getDevices();
		await tailscaleService.getAggregatedFlows(start, end);
		expect(calls).toEqual([
			'/api/tailnets',
			'/api/devices',
			`/api/flow-logs/aggregated?start=${startISO}&end=${endISO}`
		]);
		expect(get(selectedTailnetId)).toBeNull();
	});
});

describe('tailnet url and cache', () => {
	const several = [
		tailnet('default', 'example.com'),
		tailnet('lab', 'lab.example.com', 'auth failed')
	];

	it('keeps the selection in the url and preserves other params', () => {
		expect(chooseTailnet(several, null)).toBe('default');
		expect(chooseTailnet(several, 'lab')).toBe('lab');
		expect(chooseTailnet(several, 'missing')).toBe('default');
		expect(chooseTailnet([tailnet('alpha', 'Alpha'), tailnet('beta', 'Beta')], null)).toBe('alpha');
		expect(chooseTailnet([tailnet('only', 'Only')], 'only')).toBeNull();

		expect(hrefWithTailnet('/', null)).toBe('/');
		expect(hrefWithTailnet('/analytics', 'lab')).toBe('/analytics?tailnet=lab');
		expect(hrefWithTailnet('/policy', null)).toBe('/policy');

		const params = new URLSearchParams(searchWithTailnet('?query=tag:web&direction=inbound', 'lab').slice(1));
		expect(params.get('tailnet')).toBe('lab');
		expect(params.get('query')).toBe('tag:web');
		expect(params.get('direction')).toBe('inbound');

		const cleared = new URLSearchParams(searchWithTailnet('?tailnet=lab&query=tag:web', null).slice(1));
		expect(cleared.get('tailnet')).toBeNull();
		expect(cleared.get('query')).toBe('tag:web');
	});

	it('sends the selected tailnet on data requests', async () => {
		tailnetList = several;
		setTailnetSearchReader(() => '?tailnet=lab');
		await tailscaleService.getDevices();
		await tailscaleService.getAggregatedFlows(start, end);
		expect(calls.slice(1)).toEqual([
			'/api/devices?tailnet=lab',
			`/api/flow-logs/aggregated?start=${startISO}&end=${endISO}&tailnet=lab`
		]);
	});

	it('resets client caches when the tailnet changes', async () => {
		tailnetList = several;
		await tailscaleService.getDevices();
		devices.set([{ id: 'old-device' } as Device]);
		networkLogs.set([{ nodeId: 'old-device' } as NetworkLog]);
		policyGraph.set({ nodes: [], edges: [], nodeIdsBySelector: {}, warnings: [] });

		resetTailnetCaches();
		expect(get(devices)).toEqual([]);
		expect(get(networkLogs)).toEqual([]);
		expect(get(policyGraph)).toBeNull();
		expect(get(statsSummary)).toBeNull();

		devices.set([{ id: 'old-device' } as Device]);
		calls.length = 0;
		await selectTailnet('lab');
		expect(get(selectedTailnetId)).toBe('lab');
		expect(get(devices).some((device) => device.id === 'old-device')).toBe(false);
		expect(calls.some((url) => url === '/api/devices?tailnet=lab')).toBe(true);
		expect(calls.some((url) => url.includes('tailnet=default'))).toBe(false);
	});

	it('reloads the new connections range for the new tailnet without loading the graph', async () => {
		tailnetList = several;
		await tailscaleService.getDevices();
		calls.length = 0;
		setTailnetPathReader(() => '/new');

		await selectTailnet('lab');

		expect(calls).toContain('/api/flow-logs/range?tailnet=lab');
		expect(calls.some((url) => url.includes('/flow-logs/aggregated'))).toBe(false);
		expect(calls.some((url) => url.includes('/api/devices'))).toBe(false);
	});
});
