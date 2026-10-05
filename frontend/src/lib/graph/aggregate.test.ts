import { describe, expect, it } from 'vitest';
import workerSource from './graph-layout.worker.ts?raw';
import type { NetworkLink, NetworkNode } from '#lib/types';
import {
	LARGE_LAYOUT_SPACING,
	buildRenderModel,
	cullToViewport,
	groupIdFor,
	placeModel,
	subnetLabel,
	tagKey
} from './aggregate';
import { handleLayoutMessage, layoutItems } from './cheap-layout';
import { layoutOffThread } from './layout-client';
import { syntheticTailnet } from './synthetic-tailnet';
import { FULL_GRAPH_NODE_THRESHOLD } from './threshold';

function device(partial: Partial<NetworkNode> & Pick<NetworkNode, 'id'>): NetworkNode {
	return {
		ip: '',
		displayName: partial.id,
		nodeType: 'ip',
		totalBytes: partial.totalBytes ?? 100,
		txBytes: partial.totalBytes ?? 100,
		rxBytes: 0,
		connections: 1,
		tags: [],
		isTailscale: true,
		ips: partial.ip ? [partial.ip] : [],
		incomingPorts: new Set<number>(),
		outgoingPorts: new Set<number>(),
		protocols: new Set<string>(['tcp']),
		isVIPService: false,
		...partial
	};
}

function link(source: string, target: string, totalBytes = 1000): NetworkLink {
	const [left, right] = source < target ? [source, target] : [target, source];
	return {
		id: `${left}<->${right}|virtual`,
		source: left,
		target: right,
		originalSource: source,
		originalTarget: target,
		totalBytes,
		txBytes: totalBytes,
		rxBytes: 0,
		packets: 1,
		protocol: 'tcp',
		trafficType: 'virtual',
		ports: new Set<number>([443])
	};
}

describe('graph grouping', () => {
	const webA = device({ id: 'web-a', displayName: 'web-a', tags: ['tailscale', 'tag:web'], user: 'alice', ip: '100.64.0.1', totalBytes: 400 });
	const webB = device({ id: 'web-b', displayName: 'web-b', tags: ['tag:web'], user: 'bob', ip: '100.64.0.2', totalBytes: 600 });
	const lone = device({ id: 'lone', displayName: 'lone', tags: ['tag:db'], user: 'alice', ip: '10.1.1.5' });
	const carolA = device({ id: 'carol-a', displayName: 'carol-a', user: 'carol', ip: '10.2.0.4', totalBytes: 50 });
	const carolB = device({ id: 'carol-b', displayName: 'carol-b', user: 'carol', ip: '10.9.9.9', totalBytes: 70 });
	const lanA = device({ id: 'lan-a', displayName: 'lan-a', ip: '192.168.1.4' });
	const lanB = device({ id: 'lan-b', displayName: 'lan-b', ip: '192.168.1.9' });
	const v6A = device({ id: 'v6-a', displayName: 'v6-a', ip: 'fd7a:115c:a1e0:1::1' });
	const v6B = device({ id: 'v6-b', displayName: 'v6-b', ip: 'fd7a:115c:a1e0:1::2' });
	const outside = device({ id: 'outside', displayName: 'outside', ip: '8.8.8.8' });

	const nodes = [webA, webB, lone, carolA, carolB, lanA, lanB, v6A, v6B, outside];
	const edges = [
		link('web-a', 'web-b', 100),
		link('web-a', 'lone', 250),
		link('carol-a', 'lan-a', 40),
		link('v6-a', 'v6-b', 10),
		link('lan-a', 'outside', 5)
	];

	it('collapses shared tags, then users, then subnets', () => {
		expect(tagKey(device({ id: 'multi', tags: ['tag:web', 'tag:api'] }))?.label).toBe('tag:api');
		expect(tagKey(webA)?.label).toBe('tag:web');
		expect(subnetLabel('192.168.1.9')).toBe('192.168.1.0/24');
		expect(subnetLabel('fd7a:115c:a1e0:1::2')).toBe('fd7a:115c:a1e0:1::/64');

		const model = buildRenderModel(nodes, edges, new Set());
		const ids = model.nodes.map((node) => node.id);
		expect(ids).toContain(groupIdFor({ kind: 'tag', key: 'web', label: 'tag:web' }));
		expect(ids).not.toContain('web-a');
		expect(ids).not.toContain('web-b');
		expect(ids).toContain('lone');
		expect(ids).toContain(groupIdFor({ kind: 'user', key: 'carol', label: 'carol' }));
		expect(ids).toContain(groupIdFor({ kind: 'subnet', key: '192.168.1.0/24', label: '192.168.1.0/24' }));
		expect(ids).toContain(groupIdFor({ kind: 'subnet', key: 'fd7a:115c:a1e0:1::/64', label: 'fd7a:115c:a1e0:1::/64' }));
		expect(ids).toContain('outside');

		const web = model.nodes.find((node) => node.id === 'group:tag:web');
		expect(web?.memberIds).toEqual(['web-a', 'web-b']);
		expect(web?.totalBytes).toBe(1000);
		expect(web?.memberCount).toBe(2);

		const internal = model.edges.find((edge) => edge.source === 'group:tag:web' && edge.target === 'group:tag:web');
		expect(internal).toBeUndefined();
		const toLone = model.edges.find((edge) => edge.source === 'group:tag:web' && edge.target === 'lone');
		expect(toLone?.totalBytes).toBe(250);
	});

	it('expands a group back into its devices and restores their edges', () => {
		const collapsed = buildRenderModel(nodes, edges, new Set());
		expect(collapsed.nodes.some((node) => node.id === 'group:tag:web')).toBe(true);

		const expanded = buildRenderModel(nodes, edges, new Set(['group:tag:web']));
		const ids = expanded.nodes.map((node) => node.id);
		expect(ids).toContain('web-a');
		expect(ids).toContain('web-b');
		expect(ids).not.toContain('group:tag:web');
		expect(ids).toContain('lone');
		expect(expanded.groupCount).toBe(collapsed.groupCount - 1);

		const between = expanded.edges.find(
			(edge) =>
				(edge.source === 'web-a' && edge.target === 'web-b') ||
				(edge.source === 'web-b' && edge.target === 'web-a')
		);
		expect(between?.totalBytes).toBe(100);
		const toLone = expanded.edges.find(
			(edge) =>
				(edge.source === 'web-a' && edge.target === 'lone') ||
				(edge.source === 'lone' && edge.target === 'web-a')
		);
		expect(toLone?.totalBytes).toBe(250);
	});

	it('does not regroup an expanded tag by user', () => {
		const sameUser = [
			device({ id: 'a', tags: ['tag:web'], user: 'alice', ip: '10.0.0.1' }),
			device({ id: 'b', tags: ['tag:web'], user: 'alice', ip: '10.8.0.1' })
		];
		const expanded = buildRenderModel(sameUser, [], new Set(['group:tag:web']));
		expect(expanded.nodes.map((node) => node.id).sort()).toEqual(['a', 'b']);
		expect(expanded.groupCount).toBe(0);
	});
});

describe('large graph mount cap', () => {
	const viewport = { x: 0, y: 0, width: 1280, height: 800 };

	it('mounts a bounded set for a 20k tagged tailnet', () => {
		const graph = syntheticTailnet(20_000);
		const started = performance.now();
		const model = buildRenderModel(graph.nodes, graph.edges, new Set());
		const placed = placeModel(model, LARGE_LAYOUT_SPACING);
		const visible = cullToViewport(placed, model.edges, viewport);
		const elapsed = performance.now() - started;

		expect(model.deviceCount).toBe(20_000);
		expect(model.groupCount).toBe(40);
		expect(model.nodes).toHaveLength(40);
		expect(visible.nodes.length).toBeGreaterThan(0);
		expect(visible.nodes.length).toBeLessThanOrEqual(model.nodes.length);
		expect(visible.nodes.length).toBeLessThan(FULL_GRAPH_NODE_THRESHOLD);
		expect(elapsed).toBeLessThan(750);
		console.log(
			`PERF tagged ${JSON.stringify({
				elapsedMs: Math.round(elapsed * 10) / 10,
				mounted: visible.nodes.length,
				groups: model.groupCount,
				devices: model.deviceCount
			})}`
		);
	});

	it('still mounts only the viewport when grouping cannot collapse nodes', () => {
		const graph = syntheticTailnet(20_000, 'unique');
		const started = performance.now();
		const model = buildRenderModel(graph.nodes, graph.edges, new Set());
		const placed = placeModel(model);
		const visible = cullToViewport(placed, model.edges, viewport);
		const elapsed = performance.now() - started;

		expect(model.groupCount).toBe(0);
		expect(model.nodes).toHaveLength(20_000);
		expect(visible.nodes.length).toBeGreaterThan(0);
		expect(visible.nodes.length).toBeLessThan(80);
		expect(elapsed).toBeLessThan(750);
		console.log(
			`PERF unique ${JSON.stringify({
				elapsedMs: Math.round(elapsed * 10) / 10,
				mounted: visible.nodes.length,
				items: model.nodes.length
			})}`
		);
	});

	it('brings a searched device back by expanding its group', () => {
		const graph = syntheticTailnet(2_000);
		const match = graph.nodes.find((node) => node.displayName === 'node-7');
		expect(match).toBeTruthy();
		const key = tagKey(match!);
		expect(key).toBeTruthy();
		const groupId = groupIdFor(key!);
		const collapsed = buildRenderModel(graph.nodes, graph.edges, new Set());
		expect(collapsed.nodes.some((node) => node.id === match!.id)).toBe(false);
		const expanded = buildRenderModel(graph.nodes, graph.edges, new Set([groupId]));
		expect(expanded.nodes.some((node) => node.id === match!.id)).toBe(true);
	});
});

describe('cheap layout worker', () => {
	it('places items on a grid off the caller when Worker is missing', async () => {
		const items = [
			{ id: 'a', width: 220, height: 108 },
			{ id: 'b', width: 220, height: 108 },
			{ id: 'c', width: 220, height: 108 }
		];
		const direct = layoutItems(items, LARGE_LAYOUT_SPACING);
		expect(handleLayoutMessage({ requestId: 4, items, spacing: LARGE_LAYOUT_SPACING })).toEqual({
			requestId: 4,
			positions: direct
		});
		expect(direct[0]).toEqual({ id: 'a', x: 0, y: 0 });
		expect(direct[1].x).toBeGreaterThan(0);
		const result = await layoutOffThread(items, LARGE_LAYOUT_SPACING);
		expect(result.engine).toBe('main');
		expect(result.positions).toEqual(direct);
	});

	it('runs that layout from the worker entrypoint', () => {
		expect(workerSource).toContain('handleLayoutMessage');
		expect(workerSource).toContain('postMessage');
	});
});
