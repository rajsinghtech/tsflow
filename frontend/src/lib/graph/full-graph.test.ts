import { describe, expect, it } from 'vitest';
import type { Edge, Node } from '@xyflow/svelte';
import type { NetworkLink, NetworkNode, TrafficType } from '#lib/types';
import { buildElkLayoutInput, calculateNodeDimensions } from './elk-input';
import { edgeStyle, toFlowElements } from './full-graph';
import { syntheticTailnet } from './synthetic-tailnet';
import { FULL_GRAPH_NODE_THRESHOLD, usesFullGraph } from './threshold';

function device(partial: Partial<NetworkNode> & Pick<NetworkNode, 'id'>): NetworkNode {
	return {
		ip: '10.0.0.1',
		displayName: partial.id,
		nodeType: 'ip',
		totalBytes: 0,
		txBytes: 0,
		rxBytes: 0,
		connections: 0,
		tags: [],
		isTailscale: true,
		ips: ['10.0.0.1'],
		incomingPorts: new Set<number>(),
		outgoingPorts: new Set<number>(),
		protocols: new Set<string>(),
		isVIPService: false,
		...partial
	};
}

function link(
	source: string,
	target: string,
	trafficType: TrafficType,
	totalBytes: number
): NetworkLink {
	const [left, right] = source < target ? [source, target] : [target, source];
	return {
		id: `${left}<->${right}|${trafficType}`,
		source: left,
		target: right,
		originalSource: source,
		originalTarget: target,
		totalBytes,
		txBytes: totalBytes,
		rxBytes: 0,
		packets: 1,
		protocol: 'tcp',
		trafficType,
		ports: new Set<number>([443])
	};
}

// Frozen copy of the mapping NetworkGraph used before the large-graph split.
function legacyEdgeStyle(edge: { trafficType: TrafficType; totalBytes: number }, dimmed = false): string {
	let strokeColor = 'var(--color-muted-foreground)';
	{
		switch (edge.trafficType) {
			case 'virtual':
				strokeColor = 'var(--color-traffic-virtual)';
				break;
			case 'subnet':
				strokeColor = 'var(--color-traffic-subnet)';
				break;
			case 'physical':
				strokeColor = 'var(--color-traffic-physical)';
				break;
		}
	}
	let strokeWidth = 1;
	if (edge.totalBytes > 10000000) strokeWidth = 4;
	else if (edge.totalBytes > 1000000) strokeWidth = 3;
	else if (edge.totalBytes > 100000) strokeWidth = 2;
	const opacity = dimmed ? 0.15 : 1;
	return `stroke: ${strokeColor}; stroke-width: ${strokeWidth}px; opacity: ${opacity};`;
}

function legacyFlowElements(nodes: NetworkNode[], edges: NetworkLink[]): { nodes: Node[]; edges: Edge[] } {
	return {
		nodes: nodes.map((node) => ({
			id: node.id,
			type: 'network',
			position: { x: 0, y: 0 },
			data: {
				label: node.displayName,
				...node
			}
		})),
		edges: edges.map((edge) => ({
			id: edge.id,
			source: edge.source,
			target: edge.target,
			type: 'default',
			style: legacyEdgeStyle(edge)
		}))
	};
}

function legacyDimensions(node: Node): { width: number; height: number } {
	let width = 200;
	let height = 80;
	if (node.data) {
		const data = node.data as any;
		const displayName = data.displayName || data.label || data.id || '';
		width = Math.max(width, displayName.length * 8 + 60);
		const ips = data.ips || [];
		const ipv4Count = Math.min(2, ips.filter((ip: string) => !ip.includes(':')).length);
		const ipv6Count = Math.min(2, ips.filter((ip: string) => ip.includes(':')).length);
		height += (ipv4Count + ipv6Count) * 16;
		if (data.user) height += 20;
		if (data.totalBytes > 1000000) {
			width *= 1.1;
			height *= 1.05;
		}
		if (data.connections > 10) {
			width *= 1.05;
			height *= 1.1;
		}
	}
	return {
		width: Math.ceil(Math.min(Math.max(width, 180), 350)),
		height: Math.ceil(Math.min(Math.max(height, 80), 200))
	};
}

function legacyElkInput(nodes: Node[], edges: Edge[]) {
	const layoutOptions: Record<string, string> = {
		'elk.algorithm': 'layered',
		'elk.spacing.nodeNode': '150',
		'elk.spacing.componentComponent': '200',
		'elk.separateConnectedComponents': 'true',
		'elk.padding': '[top=50,left=50,bottom=50,right=50]',
		'elk.edgeRouting': 'SPLINES',
		'elk.direction': 'DOWN',
		'elk.layered.spacing.nodeNodeBetweenLayers': '200',
		'elk.layered.crossingMinimization.strategy': 'LAYER_SWEEP',
		'elk.layered.nodePlacement.strategy': 'NETWORK_SIMPLEX'
	};
	return {
		id: 'root',
		layoutOptions,
		children: nodes.map((node) => {
			const dimensions = legacyDimensions(node);
			return { id: node.id, width: dimensions.width, height: dimensions.height };
		}),
		edges: edges.map((edge) => ({
			id: edge.id,
			sources: [edge.source],
			targets: [edge.target]
		}))
	};
}

const sampleNodes = [
	device({ id: 'alpha', displayName: 'alpha', ips: ['10.1.1.1'], ip: '10.1.1.1', totalBytes: 10, connections: 1 }),
	device({
		id: 'busy',
		displayName: 'x'.repeat(40),
		user: 'alice',
		ips: ['10.0.0.1', '10.0.0.2', '10.0.0.3', 'fd7a:115c:a1e0::1', 'fd7a:115c:a1e0::2'],
		ip: '10.0.0.1',
		totalBytes: 2_000_000,
		connections: 12,
		tags: ['tailscale', 'tag:web']
	})
];

const sampleEdges = [
	link('alpha', 'busy', 'virtual', 100),
	link('alpha', 'busy', 'subnet', 100_001),
	link('alpha', 'busy', 'physical', 1_000_001),
	link('alpha', 'busy', 'exit', 10_000_001)
];

describe('full graph layout input', () => {
	it('keeps homelab graphs on the ELK path and large tailnets off it', () => {
		expect(FULL_GRAPH_NODE_THRESHOLD).toBeGreaterThanOrEqual(100);
		expect(FULL_GRAPH_NODE_THRESHOLD).toBeLessThan(20_000);
		expect(usesFullGraph(sampleNodes.length)).toBe(true);
		expect(usesFullGraph(FULL_GRAPH_NODE_THRESHOLD)).toBe(true);
		expect(usesFullGraph(FULL_GRAPH_NODE_THRESHOLD + 1)).toBe(false);
		expect(usesFullGraph(20_000)).toBe(false);
	});

	it('builds the same nodes, edges, and ELK input as the pre-change graph', () => {
		const flow = toFlowElements(sampleNodes, sampleEdges);
		const legacy = legacyFlowElements(sampleNodes, sampleEdges);
		expect(flow).toEqual(legacy);

		const elkInput = buildElkLayoutInput(flow.nodes, flow.edges, { algorithm: 'layered', nodeSpacing: 150 });
		expect(elkInput).toEqual(legacyElkInput(legacy.nodes, legacy.edges));
		expect(calculateNodeDimensions(flow.nodes[0])).toEqual({ width: 200, height: 96 });
		expect(calculateNodeDimensions(flow.nodes[1])).toEqual({ width: 350, height: 190 });
		expect(edgeStyle(sampleEdges[3], true)).toBe(
			'stroke: var(--color-muted-foreground); stroke-width: 4px; opacity: 0.15;'
		);
	});

	it('matches the legacy flow and ELK input for a 500 node tailnet', () => {
		const graph = syntheticTailnet(500);
		expect(usesFullGraph(graph.nodes.length)).toBe(true);
		const flow = toFlowElements(graph.nodes, graph.edges);
		const legacy = legacyFlowElements(graph.nodes, graph.edges);
		expect(flow.nodes).toHaveLength(500);
		expect(flow.edges).toHaveLength(499);
		expect(flow).toEqual(legacy);
		expect(buildElkLayoutInput(flow.nodes, flow.edges, { algorithm: 'layered', nodeSpacing: 150 })).toEqual(
			legacyElkInput(legacy.nodes, legacy.edges)
		);
	});
});
