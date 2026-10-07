import type { ElkNode, ElkExtendedEdge, LayoutOptions } from 'elkjs/lib/elk.bundled.js';
import type { Node, Edge } from '@xyflow/svelte';

export interface ElkLayoutOptions {
	nodeSpacing?: number;
	algorithm?: 'layered' | 'stress' | 'mrtree' | 'radial' | 'force';
}

// Network-simplex placement stays under about two seconds through this size.
// Past it, or once the edge count makes that placer expensive, the layout stays
// layered but uses the faster Brandes-Köpf placement. Spline routes are not
// drawn (the canvas paints its own curves), so large graphs skip them too.
export const QUALITY_LAYOUT_MAX_NODES = 300;
export const QUALITY_LAYOUT_MAX_EDGES = 1500;

export function usesQualityLayout(nodeCount: number, edgeCount: number): boolean {
	return nodeCount <= QUALITY_LAYOUT_MAX_NODES && edgeCount <= QUALITY_LAYOUT_MAX_EDGES;
}

const DEFAULT_NODE_WIDTH = 200;
const DEFAULT_NODE_HEIGHT = 80;

// Same dimension rules the homelab graph has always sent to ELK.
export function calculateNodeDimensions(node: Node): { width: number; height: number } {
	let width = DEFAULT_NODE_WIDTH;
	let height = DEFAULT_NODE_HEIGHT;

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

export function layeredLayoutOptions(
	options: ElkLayoutOptions = {},
	size?: { nodes: number; edges: number }
): LayoutOptions {
	const quality = size == null || usesQualityLayout(size.nodes, size.edges);
	const layoutOptions: LayoutOptions = {
		'elk.algorithm': options.algorithm || 'layered',
		'elk.spacing.nodeNode': (options.nodeSpacing || 150).toString(),
		'elk.spacing.componentComponent': '200',
		'elk.separateConnectedComponents': 'true',
		'elk.padding': '[top=50,left=50,bottom=50,right=50]',
		'elk.edgeRouting': quality ? 'SPLINES' : 'POLYLINE'
	};

	if (options.algorithm === 'layered' || !options.algorithm) {
		layoutOptions['elk.direction'] = 'DOWN';
		layoutOptions['elk.layered.spacing.nodeNodeBetweenLayers'] = '200';
		layoutOptions['elk.layered.crossingMinimization.strategy'] = 'LAYER_SWEEP';
		layoutOptions['elk.layered.nodePlacement.strategy'] = quality ? 'NETWORK_SIMPLEX' : 'BRANDES_KOEPF';
	}

	return layoutOptions;
}

export function buildElkLayoutInput(
	nodes: Node[],
	edges: Edge[],
	options: ElkLayoutOptions = {}
): ElkNode {
	const layoutOptions = layeredLayoutOptions(options, { nodes: nodes.length, edges: edges.length });

	return {
		id: 'root',
		layoutOptions,
		children: nodes.map((node) => {
			const dimensions = calculateNodeDimensions(node);
			return {
				id: node.id,
				width: dimensions.width,
				height: dimensions.height
			};
		}),
		edges: edges.map(
			(edge): ElkExtendedEdge => ({
				id: edge.id,
				sources: [edge.source],
				targets: [edge.target]
			})
		)
	};
}
