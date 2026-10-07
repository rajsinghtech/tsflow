import { describe, expect, it } from 'vitest';
import type { NetworkLink, NetworkNode } from '#lib/types';
import { buildElkLayoutInput } from './elk-input';
import {
	buildRenderModel,
	cullToViewport,
	groupIdFor,
	groupIdsContaining,
	subnetLabel,
	tagKey
} from './aggregate';
import { GROUP_LAYOUT_OPTIONS, boundaryEdges, boxesOverlap, buildCompoundGraph, modelToFlow } from './elk-place';
import { layeredLayoutOptions } from './elk-input';
import { toFlowElements } from './full-graph';
import { realisticTailnet, syntheticTailnet } from './synthetic-tailnet';
import ELK from 'elkjs/lib/elk.bundled.js';

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

	it('names the collapsed group that hides a searched device', () => {
		const model = buildRenderModel(nodes, edges, new Set());
		const open = groupIdsContaining(model, new Set(['web-a', 'carol-b']));
		expect(open).toContain(groupIdFor({ kind: 'tag', key: 'web', label: 'tag:web' }));
		expect(open).toContain(groupIdFor({ kind: 'user', key: 'carol', label: 'carol' }));
		expect(open).not.toContain('web-a');
		const revealed = buildRenderModel(nodes, edges, new Set(open));
		expect(revealed.nodes.map((node) => node.id)).toEqual(expect.arrayContaining(['web-a', 'web-b', 'carol-a', 'carol-b']));
		expect(revealed.nodes.map((node) => node.id)).not.toContain('group:tag:web');
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

describe('realistic tailnet', () => {
	it('builds hubs, skewed traffic, and a mesh instead of a ring', () => {
		const graph = realisticTailnet(20_000);
		expect(graph.nodes).toHaveLength(20_000);
		const tags = new Set(graph.nodes.flatMap((node) => node.tags.filter((tag) => tag.startsWith('tag:'))));
		const users = new Set(graph.nodes.map((node) => node.user).filter(Boolean));
		expect(tags.size).toBeGreaterThan(80);
		expect(users.size).toBeGreaterThan(100);
		expect(graph.nodes.some((node) => node.tags.includes('tag:k8s'))).toBe(true);
		expect(graph.nodes.some((node) => node.tags.includes('tag:dns'))).toBe(true);
		expect(graph.nodes.some((node) => node.id.startsWith('router-'))).toBe(true);

		const model = buildRenderModel(graph.nodes, graph.edges, new Set());
		expect(model.groupCount).toBeGreaterThan(12);
		expect(model.groupCount).toBeLessThan(40);
		const degree = new Map<string, number>();
		for (const edge of model.edges) {
			degree.set(edge.source, (degree.get(edge.source) ?? 0) + 1);
			degree.set(edge.target, (degree.get(edge.target) ?? 0) + 1);
		}
		expect(Math.max(...degree.values())).toBeGreaterThan(4);

		const bytes = graph.edges.map((edge) => edge.totalBytes).sort((a, b) => a - b);
		const mid = bytes[Math.floor(bytes.length / 2)];
		expect(bytes[bytes.length - 1]).toBeGreaterThan(mid * 10);

		const k8s = new Set(graph.nodes.filter((node) => node.tags.includes('tag:k8s')).map((node) => node.id));
		const internal = graph.edges.filter((edge) => k8s.has(edge.source) && k8s.has(edge.target));
		expect(internal.length).toBeGreaterThan(k8s.size);

		const small = realisticTailnet(1000);
		expect(small.nodes).toHaveLength(1000);
		expect(small.edges.length).toBeLessThanOrEqual(2400);
		expect(buildRenderModel(small.nodes, small.edges, new Set()).groupCount).toBeGreaterThan(10);
	});
});

describe('grouped ELK input', () => {
	it('asks ELK for the same layered layout the homelab graph uses, on groups', () => {
		const graph = realisticTailnet(20_000);
		const model = buildRenderModel(graph.nodes, graph.edges, new Set());
		const devices = new Map(graph.nodes.map((node) => [node.id, node]));
		const flow = modelToFlow(model, devices);
		const input = buildElkLayoutInput(flow.nodes, flow.edges, GROUP_LAYOUT_OPTIONS);
		const homelab = buildElkLayoutInput(
			toFlowElements(graph.nodes.slice(0, 2), graph.edges.slice(0, 1)).nodes,
			toFlowElements(graph.nodes.slice(0, 2), graph.edges.slice(0, 1)).edges,
			GROUP_LAYOUT_OPTIONS
		);
		expect(input.layoutOptions).toEqual(homelab.layoutOptions);
		expect(input.children?.length).toBe(model.nodes.length);
		expect(input.edges?.length).toBeGreaterThan(0);
	});

	it('puts an opened hub inside a compound and routes outside edges to that boundary', async () => {
		const graph = realisticTailnet(1000);
		const devices = new Map(graph.nodes.map((node) => [node.id, node]));
		const collapsed = buildRenderModel(graph.nodes, graph.edges, new Set());
		const group = collapsed.nodes.find((node) => node.id === 'group:tag:k8s');
		expect(group).toBeTruthy();
		const opened = buildRenderModel(graph.nodes, graph.edges, new Set(['group:tag:k8s']));
		const compound = buildCompoundGraph(opened, devices, 'group:tag:k8s', group!.memberIds);
		const homelab = layeredLayoutOptions(GROUP_LAYOUT_OPTIONS);
		for (const [key, value] of Object.entries(homelab)) {
			expect(compound.layoutOptions?.[key]).toBe(value);
		}
		expect(compound.layoutOptions?.['elk.hierarchyHandling']).toBe('INCLUDE_CHILDREN');
		const cluster = compound.children?.find((child) => child.id === 'group:tag:k8s');
		expect(cluster?.children?.map((child) => child.id).sort()).toEqual([...group!.memberIds].sort());
		expect(cluster?.edges?.length).toBeGreaterThan(0);
		for (const edge of compound.edges ?? []) {
			expect(group!.memberIds.includes(String(edge.sources[0]))).toBe(false);
			expect(group!.memberIds.includes(String(edge.targets[0]))).toBe(false);
		}
		const outside = boundaryEdges(opened.edges, new Set(group!.memberIds), 'group:tag:k8s');
		expect(outside.some((edge) => edge.source === 'group:tag:k8s' || edge.target === 'group:tag:k8s')).toBe(true);

		const elk = new ELK();
		const laid = await elk.layout(compound);
		const parent = laid.children?.find((child) => child.id === 'group:tag:k8s');
		expect(parent?.width).toBeGreaterThan(0);
		const top = (laid.children ?? []).map((child) => ({
			x: child.x ?? 0,
			y: child.y ?? 0,
			width: child.width ?? 0,
			height: child.height ?? 0
		}));
		for (let i = 0; i < top.length; i++) {
			for (let j = i + 1; j < top.length; j++) {
				expect(boxesOverlap(top[i], top[j])).toBe(false);
			}
		}
		const memberBoxes = (parent?.children ?? []).map((member) => ({
			x: member.x ?? 0,
			y: member.y ?? 0,
			width: member.width ?? 0,
			height: member.height ?? 0
		}));
		for (const member of memberBoxes) {
			expect(member.x).toBeGreaterThanOrEqual(-1);
			expect(member.y).toBeGreaterThanOrEqual(-1);
			expect(member.x + member.width).toBeLessThanOrEqual((parent?.width ?? 0) + 1);
			expect(member.y + member.height).toBeLessThanOrEqual((parent?.height ?? 0) + 1);
		}
		for (let i = 0; i < memberBoxes.length; i++) {
			for (let j = i + 1; j < memberBoxes.length; j++) {
				expect(boxesOverlap(memberBoxes[i], memberBoxes[j])).toBe(false);
			}
		}
		const xs = new Set(memberBoxes.map((box) => Math.round(box.x / 40)));
		const ys = new Set(memberBoxes.map((box) => Math.round(box.y / 40)));
		expect(xs.size).toBeGreaterThan(1);
		expect(ys.size).toBeGreaterThan(1);
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
