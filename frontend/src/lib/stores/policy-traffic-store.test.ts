import { describe, expect, it } from 'vitest';
import { matchPolicyRulesForEdge } from './policy-traffic-matcher';
import type { AccessEdgeMeta, GraphEdge, PolicyGraph } from '$lib/policy-engine/types';
import type { NetworkLink, NetworkLog, TrafficEntry } from '$lib/types';
import { processNetworkLogs } from '$lib/utils/network-processor';

const SOURCE_NODE = 'client';
const TARGET_NODE = 'server';
const SOURCE_TAG = 'tag:clients';
const TARGET_TAG = 'tag:servers';

function accessEdge(
	source: string,
	target: string,
	meta: Partial<AccessEdgeMeta> = {},
	type: GraphEdge['type'] = 'grant'
): GraphEdge {
	return {
		id: 'access:0',
		type,
		source,
		target,
		meta: {
			ruleRef: { section: type === 'grant' ? 'grants' : 'acls', index: 0 },
			...meta
		}
	};
}

function policyContext(...edges: GraphEdge[]) {
	const graph: PolicyGraph = {
		nodes: [],
		edges,
		nodeIdsBySelector: {},
		warnings: []
	};
	return { graph, groupMap: new Map<string, string[]>(), deviceContext: new Map() };
}

function link(overrides: Partial<NetworkLink> = {}): NetworkLink {
	return {
		id: 'client<->server|virtual',
		source: SOURCE_NODE,
		target: TARGET_NODE,
		originalSource: SOURCE_NODE,
		originalTarget: TARGET_NODE,
		totalBytes: 100,
		txBytes: 100,
		rxBytes: 0,
		packets: 1,
		protocol: 'tcp',
		trafficType: 'virtual',
		ports: new Set([443]),
		...overrides
	};
}

function direction(source: string, target: string, protocol: NetworkLink['protocol'], ports: number[]) {
	return { source, target, protocol, ports: new Set(ports) };
}

function traffic(
	src: string,
	dst: string,
	proto: number,
	port: number,
	bytes = 100,
	directional = true
): TrafficEntry {
	return {
		proto,
		src,
		dst,
		txPkts: 1,
		txBytes: bytes,
		rxPkts: 0,
		rxBytes: 0,
		ports: [{ proto, port, bytes }],
		...(directional
			? { directional: { protocolBytes: { [proto]: bytes }, ports: [{ proto, port, bytes }] } }
			: {})
	};
}

function networkLog(nodeId: string, entry: TrafficEntry): NetworkLog {
	return {
		logged: '2026-01-01T00:00:00.000Z',
		nodeId,
		start: '2026-01-01T00:00:00.000Z',
		end: '2026-01-01T00:01:00.000Z',
		virtualTraffic: [entry],
		subnetTraffic: [],
		physicalTraffic: []
	};
}

describe('directional policy matching', () => {
	it('does not match a forward tcp:443 rule against reverse-only udp:53 traffic', () => {
		const context = policyContext(accessEdge('*', '*', { ip: ['tcp:443'] }));

		const matches = matchPolicyRulesForEdge(
			link({
				protocol: 'udp',
				ports: new Set([53]),
				directions: [direction(TARGET_NODE, SOURCE_NODE, 'udp', [53])]
			}),
			context,
			[SOURCE_TAG],
			undefined,
			[TARGET_TAG],
			undefined
		);

		expect(matches).toEqual([]);
	});

	it('matches a reverse selector using the observed reverse direction', () => {
		const context = policyContext(accessEdge(TARGET_TAG, SOURCE_TAG, { ip: ['udp:53'] }));

		const matches = matchPolicyRulesForEdge(
			link({
				protocol: 'udp',
				ports: new Set([53]),
				directions: [direction(TARGET_NODE, SOURCE_NODE, 'udp', [53])]
			}),
			context,
			[SOURCE_TAG],
			undefined,
			[TARGET_TAG],
			undefined
		);

		expect(matches).toHaveLength(1);
		expect(matches[0]).toMatchObject({ source: TARGET_TAG, target: SOURCE_TAG });
	});

	it('matches a tag owner selector from device policy context', () => {
		const owner = 'alice@example.com';
		const context = policyContext(accessEdge(owner, TARGET_TAG, { ip: ['tcp:443'] }));
		context.deviceContext.set('10.0.0.1', {
			groups: [],
			tagOwners: [owner],
			autogroups: []
		});

		const matches = matchPolicyRulesForEdge(
			link(),
			context,
			[],
			undefined,
			[TARGET_TAG],
			undefined,
			['10.0.0.1'],
			['10.0.0.2']
		);

		expect(matches).toHaveLength(1);
		expect(matches[0]).toMatchObject({ source: owner, target: TARGET_TAG });
	});

	it('keeps legacy aggregate links on their original source-to-target behavior', () => {
		const context = policyContext(accessEdge(SOURCE_TAG, TARGET_TAG, { ip: ['tcp:443'] }));

		const matches = matchPolicyRulesForEdge(
			link({
				protocol: 'tcp',
				ports: new Set([443])
			}),
			context,
			[SOURCE_TAG],
			undefined,
			[TARGET_TAG],
			undefined
		);

		expect(matches).toHaveLength(1);
	});

	it('returns one policy edge when both observed directions match it', () => {
		const context = policyContext(accessEdge('*', '*', { ip: ['tcp:443'] }));

		const matches = matchPolicyRulesForEdge(
			link({
				directions: [
					direction(SOURCE_NODE, TARGET_NODE, 'tcp', [443]),
					direction(TARGET_NODE, SOURCE_NODE, 'tcp', [443])
				]
			}),
			context,
			[SOURCE_TAG],
			undefined,
			[TARGET_TAG],
			undefined
		);

		expect(matches).toHaveLength(1);
		expect(matches[0].source).toBe('*');
	});

	it('matches self-flow traffic without duplicating the policy edge', () => {
		const context = policyContext(accessEdge('*', '*', { ip: ['tcp:443'] }));

		const selfLink = link({
			source: SOURCE_NODE,
			target: SOURCE_NODE,
			directions: [direction(SOURCE_NODE, SOURCE_NODE, 'tcp', [443])]
		});
		const matches = matchPolicyRulesForEdge(
			selfLink,
			context,
			[SOURCE_TAG],
			undefined,
			[SOURCE_TAG],
			undefined
		);

		expect(matches).toHaveLength(1);
	});
});

describe('network processor direction metadata', () => {
	it('preserves protocol and port observations separately for each direction', () => {
		const processed = processNetworkLogs([
			networkLog(SOURCE_NODE, traffic(SOURCE_NODE, TARGET_NODE, 6, 443)),
			networkLog(TARGET_NODE, traffic(TARGET_NODE, SOURCE_NODE, 17, 53))
		], []);

		expect(processed.links).toHaveLength(1);
		expect(processed.links[0].directions).toEqual([
			direction(SOURCE_NODE, TARGET_NODE, 'tcp', [443]),
			direction(TARGET_NODE, SOURCE_NODE, 'udp', [53])
		]);
	});

	it('leaves legacy processor links without directional metadata', () => {
		const processed = processNetworkLogs([
			networkLog(SOURCE_NODE, traffic(SOURCE_NODE, TARGET_NODE, 6, 443, 100, false))
		], []);

		expect(processed.links).toHaveLength(1);
		expect(processed.links[0].directions).toBeUndefined();
		expect(processed.links[0].protocol).toBe('tcp');
		expect(processed.links[0].ports).toEqual(new Set([443]));
	});
});
