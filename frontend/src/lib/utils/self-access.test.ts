import { describe, expect, it } from 'vitest';
import type { AccessEdgeMeta, GraphEdge, GraphNode } from '$lib/policy-engine/types';
import { parsePolicyText } from '$lib/policy-engine/parser';
import {
	buildPolicyFlow,
	estimatePolicyNodeSize,
	policyEdgesToXYFlow,
	SELF_ACCESS_BADGE_ALLOWANCE
} from './policy-layout';
import { collectSelfAccessByNode, isSelfAccessEdge, summarizeSelfAccess } from './self-access';

function edge(
	source: string,
	target: string,
	type: GraphEdge['type'] = 'grant',
	meta: Partial<AccessEdgeMeta> = {},
	id = `${type}:${source}->${target}`
): GraphEdge {
	return {
		id,
		type,
		source,
		target,
		meta: {
			ruleRef: {
				section: type === 'acl' ? 'acls' : type === 'ssh' ? 'ssh' : 'grants',
				index: 0
			},
			...meta
		}
	};
}

function node(id: string, type: GraphNode['type'] = 'tag'): GraphNode {
	return { id, type, label: id, rawSelector: id };
}

describe('isSelfAccessEdge', () => {
	it('detects grant, acl, and ssh edges that start and end on the same node', () => {
		expect(isSelfAccessEdge(edge('tag:server', 'tag:server', 'grant'))).toBe(true);
		expect(isSelfAccessEdge(edge('tag:server', 'tag:server', 'acl'))).toBe(true);
		expect(isSelfAccessEdge(edge('tag:server', 'tag:server', 'ssh'))).toBe(true);
	});

	it('ignores access between different nodes', () => {
		expect(isSelfAccessEdge(edge('tag:server', 'tag:ai', 'grant', { ip: ['tcp:443'] }))).toBe(false);
		expect(isSelfAccessEdge(edge('tag:server', 'tag:ai', 'acl'))).toBe(false);
		expect(isSelfAccessEdge(edge('tag:server', 'tag:ai', 'ssh'))).toBe(false);
	});

	it('does not treat a relation loop as self-access', () => {
		expect(isSelfAccessEdge(edge('group:eng', 'group:eng', 'member-of'))).toBe(false);
		expect(isSelfAccessEdge(edge('tag:server', 'tag:server', 'owns-tag'))).toBe(false);
		expect(isSelfAccessEdge(edge('ipset:a', 'ipset:a', 'contains'))).toBe(false);
		expect(isSelfAccessEdge(edge('host', 'host', 'resolves-to'))).toBe(false);
	});
});

describe('summarizeSelfAccess', () => {
	it('returns null when the node has no self-access edge', () => {
		expect(summarizeSelfAccess('tag', [edge('tag:server', 'tag:ai')])).toBeNull();
	});

	it('describes a tag grant with ports and protocols', () => {
		const summary = summarizeSelfAccess('tag', [
			edge('tag:server', 'tag:server', 'grant', { ip: ['tcp:443', 'udp:53'] }, 'g1')
		]);
		expect(summary).toMatchObject({
			edgeIds: ['g1'],
			constraints: ['tcp:443', 'udp:53'],
			tooltip: 'This tag can reach other devices with the same tag (tcp:443, udp:53).',
			ariaLabel: 'This tag can reach other devices with the same tag (tcp:443, udp:53).'
		});
	});

	it('says all ports when a grant explicitly allows every protocol', () => {
		const summary = summarizeSelfAccess('tag', [
			edge('tag:ai', 'tag:ai', 'grant', { ip: ['*'] })
		]);
		expect(summary?.tooltip).toBe(
			'This tag can reach other devices with the same tag (all ports and protocols).'
		);
	});

	it('does not invent ports when a grant has no ip field', () => {
		const summary = summarizeSelfAccess('tag', [edge('tag:ai', 'tag:ai', 'grant')]);
		expect(summary?.tooltip).toBe('This tag can reach other devices with the same tag.');
		expect(summary?.constraints).toEqual([]);
	});

	it('lets an explicit wildcard subsume narrower grant specs', () => {
		const summary = summarizeSelfAccess('tag', [
			edge('tag:server', 'tag:server', 'grant', { ip: ['tcp:443', '*'] })
		]);
		expect(summary?.constraints).toEqual(['all ports and protocols']);
	});

	it('formats acl proto and ports, including several ports on one destination', () => {
		const summary = summarizeSelfAccess('tag', [
			edge('tag:server', 'tag:server', 'acl', { proto: 'tcp', ports: ['22'] }, 'a1')
		]);
		expect(summary?.tooltip).toBe('This tag can reach other devices with the same tag (tcp:22).');

		const many = summarizeSelfAccess('tag', [
			edge('tag:server', 'tag:server', 'acl', { proto: 'tcp', ports: ['80', '443'] })
		]);
		expect(many?.constraints).toEqual(['tcp:80,443']);
	});

	it('formats an acl that names only a protocol', () => {
		const summary = summarizeSelfAccess('tag', [
			edge('tag:server', 'tag:server', 'acl', { proto: 'icmp' })
		]);
		expect(summary?.constraints).toEqual(['icmp']);
	});

	it('formats an acl whose ports are not tied to one protocol', () => {
		const summary = summarizeSelfAccess('tag', [
			edge('tag:server', 'tag:server', 'acl', { ports: ['80', '443'] })
		]);
		expect(summary?.constraints).toEqual(['ports 80,443']);
	});

	it('keeps an invalid acl port from looking unrestricted', () => {
		const summary = summarizeSelfAccess('tag', [
			edge('tag:server', 'tag:server', 'acl', { ports: ['__invalid__'] })
		]);
		expect(summary?.constraints).toEqual(['invalid port']);
	});

	it('includes ssh users', () => {
		const summary = summarizeSelfAccess('tag', [
			edge('tag:server', 'tag:server', 'ssh', { sshUsers: ['root'] }, 's1'),
			edge('tag:server', 'tag:server', 'ssh', { sshUsers: ['ubuntu', 'root'] }, 's2')
		]);
		expect(summary?.edgeIds).toEqual(['s1', 's2']);
		expect(summary?.tooltip).toBe(
			'This tag can reach other devices with the same tag (ssh as root, ubuntu).'
		);
	});

	it('labels mixed rule kinds so ports stay attached to the right rule', () => {
		const summary = summarizeSelfAccess('tag', [
			edge('tag:server', 'tag:server', 'grant', { ip: ['tcp:443', 'udp:53'] }, 'g1'),
			edge('tag:server', 'tag:server', 'acl', { proto: 'tcp', ports: ['22'] }, 'a1'),
			edge('tag:server', 'tag:server', 'ssh', { sshUsers: ['root'] }, 's1')
		]);
		expect(summary?.tooltip).toBe(
			'This tag can reach other devices with the same tag (grant: tcp:443, udp:53; acl: tcp:22; ssh as root).'
		);
		expect(summary?.ariaLabel).toBe(summary?.tooltip);
	});

	it('dedupes repeated specs and ignores edges to other nodes', () => {
		const summary = summarizeSelfAccess('tag', [
			edge('tag:server', 'tag:server', 'grant', { ip: ['tcp:443'] }, 'g1'),
			edge('tag:server', 'tag:ai', 'grant', { ip: ['tcp:80'] }, 'g2'),
			edge('tag:server', 'tag:server', 'grant', { ip: ['tcp:443'] }, 'g3')
		]);
		expect(summary?.edgeIds).toEqual(['g1', 'g3']);
		expect(summary?.constraints).toEqual(['tcp:443']);
	});

	it('uses selector wording for groups, autogroups, and other nodes', () => {
		expect(summarizeSelfAccess('group', [edge('group:eng', 'group:eng')])?.tooltip).toBe(
			'Members of this group can reach each other.'
		);
		expect(summarizeSelfAccess('autogroup', [edge('autogroup:member', 'autogroup:member')])?.tooltip).toBe(
			'Members of this autogroup can reach each other.'
		);
		expect(summarizeSelfAccess('user', [edge('a@example.com', 'a@example.com', 'grant', { ip: ['tcp:22'] })])?.tooltip).toBe(
			'Devices matched by this selector can reach each other (tcp:22).'
		);
	});

	it('caps a long port list so one node cannot grow a huge tooltip', () => {
		const specs = Array.from({ length: 9 }, (_, index) => `tcp:${index + 1}`);
		const summary = summarizeSelfAccess('tag', [
			edge('tag:server', 'tag:server', 'grant', { ip: specs })
		]);
		expect(summary?.constraints).toEqual(specs);
		expect(summary?.tooltip).toBe(
			'This tag can reach other devices with the same tag (tcp:1, tcp:2, tcp:3, tcp:4, tcp:5, tcp:6, tcp:7, tcp:8, and 1 more).'
		);
		expect(summary?.tooltip.includes('tcp:9')).toBe(false);
	});

	it('does not mutate the edges it reads', () => {
		const edges = [edge('tag:server', 'tag:server', 'grant', { ip: ['tcp:443'] })];
		const before = JSON.stringify(edges);
		summarizeSelfAccess('tag', edges);
		expect(JSON.stringify(edges)).toBe(before);
	});
});

describe('collectSelfAccessByNode', () => {
	it('attaches each self-edge to its own node and skips everyone else', () => {
		const nodes = [node('tag:server'), node('tag:ai'), node('tag:db')];
		const edges = [
			edge('tag:server', 'tag:server', 'grant', { ip: ['tcp:443'] }, 'self-server'),
			edge('tag:server', 'tag:ai', 'grant', { ip: ['tcp:80'] }, 'cross'),
			edge('tag:ai', 'tag:ai', 'acl', { proto: 'udp', ports: ['53'] }, 'self-ai')
		];
		const summaries = collectSelfAccessByNode(nodes, edges);
		expect([...summaries.keys()]).toEqual(['tag:server', 'tag:ai']);
		expect(summaries.get('tag:server')?.edgeIds).toEqual(['self-server']);
		expect(summaries.get('tag:server')?.constraints).toEqual(['tcp:443']);
		expect(summaries.get('tag:ai')?.constraints).toEqual(['udp:53']);
		expect(summaries.has('tag:db')).toBe(false);
	});

	it('collapses many self-edges on one node into a single summary', () => {
		const edges = Array.from({ length: 40 }, (_, index) =>
			edge('tag:server', 'tag:server', 'grant', { ip: [`tcp:${index + 1}`] }, `g${index}`)
		);
		const summaries = collectSelfAccessByNode([node('tag:server')], edges);
		expect(summaries.size).toBe(1);
		expect(summaries.get('tag:server')?.edgeIds).toHaveLength(40);
	});
});

describe('policy text integration', () => {
	it('reads self grants and acls from parsed policy, including dst ports', () => {
		const parsed = parsePolicyText(`{
			"grants": [
				{"src": ["tag:server"], "dst": ["tag:server", "tag:ai"], "ip": ["tcp:443", "udp:53"]},
				{"src": ["tag:ai"], "dst": ["tag:ai"], "ip": ["*"]}
			],
			"acls": [
				{"action": "accept", "src": ["tag:server"], "dst": ["tag:server:22"], "proto": "tcp"}
			]
		}`);
		const summaries = collectSelfAccessByNode(parsed.graph.nodes, parsed.graph.edges);
		expect(summaries.get('tag:server')?.tooltip).toBe(
			'This tag can reach other devices with the same tag (grant: tcp:443, udp:53; acl: tcp:22).'
		);
		expect(summaries.get('tag:ai')?.tooltip).toBe(
			'This tag can reach other devices with the same tag (all ports and protocols).'
		);
		expect(summaries.get('tag:server')?.tooltip.includes('tcp:80')).toBe(false);
	});
});

describe('buildPolicyFlow', () => {
	const server = node('tag:server');
	const ai = node('tag:ai');
	const self = edge('tag:server', 'tag:server', 'grant', { ip: ['tcp:443'] }, 'self');
	const cross = edge('tag:server', 'tag:ai', 'grant', { ip: ['tcp:80'] }, 'cross');
	const relation = edge('autogroup:admin', 'tag:server', 'owns-tag', {}, 'owns');
	const relationLoop = edge('group:eng', 'group:eng', 'member-of', {}, 'loop');

	it('drops self-access edges and leaves every other edge unchanged', () => {
		const flow = buildPolicyFlow([server, ai, node('group:eng', 'group')], [self, cross, relation, relationLoop]);
		expect(flow.edges.map((item) => item.id)).toEqual(['cross', 'owns', 'loop']);
		expect(flow.edges[0]).toEqual(policyEdgesToXYFlow([cross])[0]);
		expect(flow.edges[1]).toEqual(policyEdgesToXYFlow([relation])[0]);
		expect(flow.edges[2]).toEqual(policyEdgesToXYFlow([relationLoop])[0]);
		expect(flow.edges[0].data).toMatchObject({
			edgeType: 'grant',
			color: '#0d9488',
			width: 2,
			meta: cross.meta
		});
	});

	it('puts one self-access summary on the source node only', () => {
		const flow = buildPolicyFlow([server, ai], [self, cross]);
		const serverData = flow.nodes.find((item) => item.id === 'tag:server')?.data as {
			selfAccess?: { tooltip: string; edgeIds: string[] };
		};
		const aiData = flow.nodes.find((item) => item.id === 'tag:ai')?.data as { selfAccess?: unknown };
		expect(serverData.selfAccess?.edgeIds).toEqual(['self']);
		expect(serverData.selfAccess?.tooltip).toContain('tcp:443');
		expect(aiData.selfAccess).toBeUndefined();
	});

	it('keeps non-self node dimensions on the previous formula', () => {
		expect(estimatePolicyNodeSize('tag:ai')).toEqual({ width: 100, height: 32 });
		expect(estimatePolicyNodeSize('tag:server')).toEqual({
			width: Math.max(100, Math.min(220, 'tag:server'.length * 7.5 + 30)),
			height: 32
		});
		expect(estimatePolicyNodeSize('x'.repeat(40))).toEqual({ width: 220, height: 32 });
		expect(estimatePolicyNodeSize('x'.repeat(40), true)).toEqual({ width: 220, height: 32 });
		expect(estimatePolicyNodeSize('tag:ai', true).height).toBe(32);
		expect(estimatePolicyNodeSize('tag:ai', true).width).toBe(
			Math.max(100, Math.min(220, 'tag:ai'.length * 7.5 + 30 + SELF_ACCESS_BADGE_ALLOWANCE))
		);
	});
});
