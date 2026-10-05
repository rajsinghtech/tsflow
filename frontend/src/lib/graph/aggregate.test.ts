import { describe, expect, it } from 'vitest';
import type { NetworkLink, NetworkNode } from '#lib/types';
import { buildElkLayoutInput } from './elk-input';
import {
	buildRenderModel,
	cullToViewport,
	groupIdFor,
	subnetLabel,
	tagKey
} from './aggregate';
import { GROUP_LAYOUT_OPTIONS, expansionSubgraph, modelToFlow, placeExpandedLayout } from './elk-place';
import { toFlowElements } from './full-graph';
import { syntheticTailnet } from './synthetic-tailnet';

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
	const webA = device({ id: 'web-a', tags: ['tailscale', 'tag:web'], user: 'alice', ip: '100.64.0.1', totalBytes: 400 });
	const webB = device({ id: 'web-b', tags: ['tag:web'], user: 'bob', ip: '100.64.0.2', totalBytes: 600 });
	const lone = device({ id: 'lone', tags: ['tag:db'], user: 'alice', ip: '10.1.1.5' });
	const carolA = device({ id: 'carol-a', user: 'carol', ip: '10.2.0.4', totalBytes: 50 });
	const carolB = device({ id: 'carol-b', user: 'carol', ip: '10.9.9.9', totalBytes: 70 });
	const lanA = device({ id: 'lan-a', ip: '192.168.1.4' });
	const lanB = device({ id: 'lan-b', ip: '192.168.1.9' });
	const outside = device({ id: 'outside', ip: '8.8.8.8' });
	const nodes = [webA, webB, lone, carolA, carolB, lanA, lanB, outside];
	const edges = [link('web-a', 'web-b', 100), link('web-a', 'lone', 250), link('carol-a', 'lan-a', 40)];

	it('collapses shared tags, then users, then subnets', () => {
		expect(tagKey(device({ id: 'multi', tags: ['tag:web', 'tag:api'] }))?.label).toBe('tag:api');
		expect(subnetLabel('192.168.1.9')).toBe('192.168.1.0/24');
		expect(subnetLabel('fd7a:115c:a1e0:1::2')).toBe('fd7a:115c:a1e0:1::/64');

		const model = buildRenderModel(nodes, edges, new Set());
		const ids = model.nodes.map((node) => node.id);
		expect(ids).toContain(groupIdFor({ kind: 'tag', key: 'web', label: 'tag:web' }));
		expect(ids).not.toContain('web-a');
		expect(ids).toContain('lone');
		expect(ids).toContain(groupIdFor({ kind: 'user', key: 'carol', label: 'carol' }));
		expect(ids).toContain(groupIdFor({ kind: 'subnet', key: '192.168.1.0/24', label: '192.168.1.0/24' }));
		const web = model.nodes.find((node) => node.id === 'group:tag:web');
		expect(web?.memberIds).toEqual(['web-a', 'web-b']);
		expect(web?.totalBytes).toBe(1000);
		expect(model.edges.find((edge) => edge.source === 'group:tag:web' && edge.target === 'group:tag:web')).toBeUndefined();
		expect(model.edges.find((edge) => edge.source === 'group:tag:web' && edge.target === 'lone')?.totalBytes).toBe(250);
	});

	it('expands a group back into its devices and restores their edges', () => {
		const expanded = buildRenderModel(nodes, edges, new Set(['group:tag:web']));
		const ids = expanded.nodes.map((node) => node.id);
		expect(ids).toContain('web-a');
		expect(ids).toContain('web-b');
		expect(ids).not.toContain('group:tag:web');
		expect(expanded.groupCount).toBe(buildRenderModel(nodes, edges, new Set()).groupCount - 1);
		expect(
			expanded.edges.find(
				(edge) =>
					(edge.source === 'web-a' && edge.target === 'lone') ||
					(edge.source === 'lone' && edge.target === 'web-a')
			)?.totalBytes
		).toBe(250);
	});

	it('does not regroup an expanded tag by user', () => {
		const sameUser = [
			device({ id: 'a', tags: ['tag:web'], user: 'alice', ip: '10.0.0.1' }),
			device({ id: 'b', tags: ['tag:web'], user: 'alice', ip: '10.8.0.1' })
		];
		const expanded = buildRenderModel(sameUser, [], new Set(['group:tag:web']));
		expect(expanded.nodes.map((node) => node.id).sort()).toEqual(['a', 'b']);
	});
});

describe('grouped ELK input', () => {
	it('asks ELK for the same layered layout the homelab graph uses, on groups', () => {
		const graph = syntheticTailnet(20_000);
		const model = buildRenderModel(graph.nodes, graph.edges, new Set());
		expect(model.deviceCount).toBe(20_000);
		expect(model.groupCount).toBe(40);
		expect(model.nodes).toHaveLength(40);

		const devices = new Map(graph.nodes.map((node) => [node.id, node]));
		const flow = modelToFlow(model, devices);
		const input = buildElkLayoutInput(flow.nodes, flow.edges, GROUP_LAYOUT_OPTIONS);
		const homelab = buildElkLayoutInput(
			toFlowElements(graph.nodes.slice(0, 2), graph.edges.slice(0, 1)).nodes,
			toFlowElements(graph.nodes.slice(0, 2), graph.edges.slice(0, 1)).edges,
			GROUP_LAYOUT_OPTIONS
		);
		expect(input.layoutOptions).toEqual(homelab.layoutOptions);
		expect(input.children).toHaveLength(40);
		expect(input.edges?.length).toBeGreaterThan(0);
		expect(input.children?.every((child) => String(child.id).startsWith('group:'))).toBe(true);
	});

	it('keeps neighboring groups fixed when a group is opened', () => {
		const graph = syntheticTailnet(80);
		const devices = new Map(graph.nodes.map((node) => [node.id, node]));
		const collapsed = buildRenderModel(graph.nodes, graph.edges, new Set());
		const group = collapsed.nodes.find((node) => node.id === 'group:tag:pool-0');
		expect(group).toBeTruthy();
		const opened = buildRenderModel(graph.nodes, graph.edges, new Set(['group:tag:pool-0']));
		const memberIds = new Set(group!.memberIds);
		const current = new Map(
			collapsed.nodes.map((node, index) => [
				node.id,
				{
					id: node.id,
					type: node.kind === 'group' ? 'group' : 'network',
					position: { x: index * 400, y: 20 },
					width: 200,
					height: 80,
					data: {}
				}
			])
		);
		const subgraph = expansionSubgraph(opened, memberIds, devices, current);
		expect(subgraph.anchorIds.length).toBeGreaterThan(0);
		expect(subgraph.nodes.some((node) => memberIds.has(node.id))).toBe(true);

		const kept = new Map([...current.entries()].map(([id, node]) => [id, { x: node.position.x, y: node.position.y }]));
		const laidOut = subgraph.nodes.map((node, index) => ({
			id: node.id,
			x: index * 10,
			y: index * 5,
			width: 200,
			height: 80
		}));
		const placed = placeExpandedLayout(laidOut, kept, memberIds, {
			x: kept.get('group:tag:pool-0')!.x,
			y: kept.get('group:tag:pool-0')!.y,
			width: 200,
			height: 80
		});
		for (const anchorId of subgraph.anchorIds) {
			expect(placed.get(anchorId)).toEqual(kept.get(anchorId));
		}
		for (const memberId of memberIds) {
			expect(placed.get(memberId)).toBeTruthy();
			expect(placed.get(memberId)).not.toEqual(kept.get(memberId));
		}
	});

	it('mounts only cards inside the viewport and does not move the others', () => {
		const nodes = [
			{ id: 'a', x: 0, y: 0, width: 200, height: 80 },
			{ id: 'b', x: 5000, y: 5000, width: 200, height: 80 }
		];
		const visible = cullToViewport(nodes, [{ id: 'e', source: 'a', target: 'b' }], {
			x: 0,
			y: 0,
			width: 1280,
			height: 800
		});
		expect(visible.nodes.map((node) => node.id)).toEqual(['a']);
		expect(visible.nodes[0]).toEqual(nodes[0]);
		expect(visible.edges).toEqual([]);
	});
});
