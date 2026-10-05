import ELK, { type ElkNode } from 'elkjs/lib/elk.bundled.js';
import type { Node, Edge } from '@xyflow/svelte';
import { FULL_GRAPH_NODE_THRESHOLD } from '#lib/graph/threshold';
import {
	buildElkLayoutInput,
	calculateNodeDimensions,
	type ElkLayoutOptions
} from '#lib/graph/elk-input';

export type { ElkLayoutOptions };

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

const ELK_NODE_LIMIT = FULL_GRAPH_NODE_THRESHOLD;
const ELK_EDGE_LIMIT = 2500;

function nodesWithElkPositions(nodes: Node[], layoutedGraph: ElkNode): Node[] {
	return nodes.map((node) => {
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

	const elkGraph = buildElkLayoutInput(nodes, edges, options);

	try {
		const layoutedGraph = await elk.layout(elkGraph);
		return { nodes: nodesWithElkPositions(nodes, layoutedGraph), edges };
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

let mainThreadElk: InstanceType<typeof ELK> | null = null;

// Same ELK input and worker as applyElkLayout, without the homelab size cap
// and without the grid fallback. Used for grouped graphs and for one opened group.
export async function runElkGraph(graph: ElkNode): Promise<ElkNode> {
	try {
		return await elk.layout(graph);
	} catch (error) {
		console.error('ELK worker failed, running ELK on the main thread:', error);
		if (!mainThreadElk) mainThreadElk = new ELK();
		return await mainThreadElk.layout(graph);
	}
}

export async function runElkLayout(
	nodes: Node[],
	edges: Edge[],
	options: ElkLayoutOptions = {}
): Promise<{ nodes: Node[]; edges: Edge[] }> {
	if (nodes.length === 0) {
		return { nodes: [], edges: [] };
	}

	const layoutedGraph = await runElkGraph(buildElkLayoutInput(nodes, edges, options));
	return { nodes: nodesWithElkPositions(nodes, layoutedGraph), edges };
}
