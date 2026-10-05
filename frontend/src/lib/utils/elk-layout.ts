import ELK, { type ElkNode, type ElkExtendedEdge, type LayoutOptions } from 'elkjs/lib/elk.bundled.js';
import type { Node, Edge } from '@xyflow/svelte';

// Track worker initialization status for debugging
let workerInitFailed = false;

// Use Web Worker for layout calculations to avoid blocking the main thread
const elk = new ELK({
	workerFactory: () => {
		try {
			const worker = new Worker(new URL('elkjs/lib/elk-worker.min.js', import.meta.url), {
				type: 'module'
			});
			worker.onerror = (e) => {
				console.error('ELK worker error:', e.message);
				workerInitFailed = true;
			};
			return worker;
		} catch (error) {
			console.error('Failed to create ELK worker:', error);
			workerInitFailed = true;
			throw error;
		}
	}
});

const DEFAULT_NODE_WIDTH = 200;
const DEFAULT_NODE_HEIGHT = 80;
const ELK_NODE_LIMIT = 1200;
const ELK_EDGE_LIMIT = 2500;

export interface ElkLayoutOptions {
	nodeSpacing?: number;
	algorithm?: 'layered' | 'stress' | 'mrtree' | 'radial' | 'force';
}

// Calculate node dimensions based on content
function calculateNodeDimensions(node: Node): { width: number; height: number } {
	let width = DEFAULT_NODE_WIDTH;
	let height = DEFAULT_NODE_HEIGHT;

	if (node.data) {
		const data = node.data as any;

		// Measure display name
		const displayName = data.displayName || data.label || data.id || '';
		width = Math.max(width, displayName.length * 8 + 60);

		// Add height for IPs (up to 2 IPv4 + 2 IPv6 displayed)
		const ips = data.ips || [];
		const ipv4Count = Math.min(2, ips.filter((ip: string) => !ip.includes(':')).length);
		const ipv6Count = Math.min(2, ips.filter((ip: string) => ip.includes(':')).length);
		height += (ipv4Count + ipv6Count) * 16;

		// Add height for user info
		if (data.user) height += 20;

		// Adjust for high traffic nodes
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

// Apply ELK layout to nodes and edges
export async function applyElkLayout(
	nodes: Node[],
	edges: Edge[],
	options: ElkLayoutOptions = {}
): Promise<{ nodes: Node[]; edges: Edge[] }> {
	if (nodes.length === 0) {
		return { nodes: [], edges: [] };
	}

	if (nodes.length > ELK_NODE_LIMIT || edges.length > ELK_EDGE_LIMIT) {
		return applyFallbackLayout(nodes, edges, options.nodeSpacing || 150);
	}

	const layoutOptions: LayoutOptions = {
		'elk.algorithm': options.algorithm || 'layered',
		'elk.spacing.nodeNode': (options.nodeSpacing || 150).toString(),
		'elk.spacing.componentComponent': '200',
		'elk.separateConnectedComponents': 'true',
		'elk.padding': '[top=50,left=50,bottom=50,right=50]',
		'elk.edgeRouting': 'SPLINES'
	};

	if (options.algorithm === 'layered' || !options.algorithm) {
		layoutOptions['elk.direction'] = 'DOWN';
		layoutOptions['elk.layered.spacing.nodeNodeBetweenLayers'] = '200';
		layoutOptions['elk.layered.crossingMinimization.strategy'] = 'LAYER_SWEEP';
		layoutOptions['elk.layered.nodePlacement.strategy'] = 'NETWORK_SIMPLEX';
	}

	const elkGraph: ElkNode = {
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

	try {
		const layoutedGraph = await elk.layout(elkGraph);

		const layoutedNodes = nodes.map((node) => {
			const layoutedNode = layoutedGraph.children?.find((n) => n.id === node.id);

			if (!layoutedNode) {
				return {
					...node,
					position: node.position || { x: 0, y: 0 }
				};
			}

			return {
				...node,
				position: {
					x: layoutedNode.x ?? 0,
					y: layoutedNode.y ?? 0
				},
				width: layoutedNode.width,
				height: layoutedNode.height
			};
		});

		return { nodes: layoutedNodes, edges };
	} catch (error) {
		const errorContext = workerInitFailed ? ' (worker initialization failed)' : '';
		console.error(`ELK layout failed${errorContext}, using fallback grid layout:`, error);
		return applyFallbackLayout(nodes, edges, options.nodeSpacing || 150);
	}
}

// Fallback grid layout
function applyFallbackLayout(
	nodes: Node[],
	edges: Edge[],
	spacing: number
): { nodes: Node[]; edges: Edge[] } {
	const cols = Math.ceil(Math.sqrt(nodes.length));

	const layoutedNodes = nodes.map((node, index) => {
		const row = Math.floor(index / cols);
		const col = index % cols;
		const dimensions = calculateNodeDimensions(node);

		return {
			...node,
			position: {
				x: col * (dimensions.width + spacing),
				y: row * (dimensions.height + spacing)
			}
		};
	});

	return { nodes: layoutedNodes, edges };
}
