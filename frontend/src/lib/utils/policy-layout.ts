import ELK, { type ElkNode, type ElkExtendedEdge, type LayoutOptions } from 'elkjs/lib/elk.bundled.js';
import type { Node, Edge } from '@xyflow/svelte';
import type { GraphNode, GraphEdge } from '$lib/policy-engine/types';

const elk = new ELK({
	workerFactory: () => {
		try {
			return new Worker(new URL('elkjs/lib/elk-worker.min.js', import.meta.url), { type: 'module' });
		} catch {
			throw new Error('Failed to create ELK worker for policy layout');
		}
	}
});

export const NODE_COLORS: Record<string, string> = {
	user: '#f59e0b',
	group: '#16a34a',
	tag: '#db2777',
	autogroup: '#7c3aed',
	host: '#2563eb',
	ipset: '#0891b2',
	service: '#dc2626',
	ip: '#4b5563',
	cidr: '#4b5563',
	wildcard: '#374151',
	unknown: '#111827'
};

export const EDGE_STYLES: Record<string, { color: string; strokeDasharray?: string; width: number; opacity: number }> = {
	grant: { color: '#0d9488', width: 2, opacity: 1 },
	acl: { color: '#0284c7', strokeDasharray: '6', width: 2, opacity: 1 },
	ssh: { color: '#b45309', strokeDasharray: '2 4', width: 2, opacity: 1 },
	'member-of': { color: '#9ca3af', width: 1, opacity: 0.4 },
	'owns-tag': { color: '#9ca3af', width: 1, opacity: 0.4 },
	contains: { color: '#9ca3af', width: 1, opacity: 0.4 },
	'resolves-to': { color: '#9ca3af', width: 1, opacity: 0.4 }
};

const FAST_RENDER_NODE_THRESHOLD = 1500;
const FAST_RENDER_EDGE_THRESHOLD = 4000;

export function policyNodesToXYFlow(nodes: GraphNode[]): Node[] {
	return nodes.map((node) => ({
		id: node.id,
		type: 'policyNode',
		position: { x: 0, y: 0 },
		data: {
			label: node.label,
			nodeType: node.type,
			rawSelector: node.rawSelector,
			color: NODE_COLORS[node.type] ?? NODE_COLORS.unknown
		}
	}));
}

export function policyEdgesToXYFlow(edges: GraphEdge[]): Edge[] {
	return edges.map((edge) => {
		const style = EDGE_STYLES[edge.type] ?? EDGE_STYLES['member-of'];
		return {
			id: edge.id,
			source: edge.source,
			target: edge.target,
			type: 'policyEdge',
			data: {
				edgeType: edge.type,
				meta: edge.meta,
				...style
			}
		};
	});
}

export function isFastRenderMode(nodeCount: number, edgeCount: number): boolean {
	return nodeCount >= FAST_RENDER_NODE_THRESHOLD || edgeCount >= FAST_RENDER_EDGE_THRESHOLD;
}

function policyNodeDimensions(node: Node): { width: number; height: number } {
	const label = (node.data as any)?.label ?? '';
	return {
		width: Math.max(100, Math.min(220, label.length * 7.5 + 30)),
		height: 32
	};
}

export async function applyPolicyElkLayout(
	nodes: Node[],
	edges: Edge[]
): Promise<{ nodes: Node[]; edges: Edge[] }> {
	if (nodes.length === 0) return { nodes: [], edges: [] };

	const layoutOptions: LayoutOptions = {
		'elk.algorithm': 'layered',
		'elk.direction': 'DOWN',
		'elk.spacing.nodeNode': '30',
		'elk.spacing.componentComponent': '80',
		'elk.separateConnectedComponents': 'true',
		'elk.padding': '[top=30,left=30,bottom=30,right=30]',
		'elk.edgeRouting': 'SPLINES',
		'elk.layered.spacing.nodeNodeBetweenLayers': '60',
		'elk.layered.crossingMinimization.strategy': 'LAYER_SWEEP',
		'elk.layered.nodePlacement.strategy': 'NETWORK_SIMPLEX'
	};

	const elkGraph: ElkNode = {
		id: 'root',
		layoutOptions,
		children: nodes.map((node) => {
			const dims = policyNodeDimensions(node);
			return { id: node.id, width: dims.width, height: dims.height };
		}),
		edges: edges.map((edge): ElkExtendedEdge => ({
			id: edge.id,
			sources: [edge.source],
			targets: [edge.target]
		}))
	};

	try {
		const result = await elk.layout(elkGraph);
		const layoutedNodes = nodes.map((node) => {
			const ln = result.children?.find((n) => n.id === node.id);
			return {
				...node,
				position: { x: ln?.x ?? 0, y: ln?.y ?? 0 },
				width: ln?.width,
				height: ln?.height
			};
		});
		return { nodes: layoutedNodes, edges };
	} catch (error) {
		console.error('Policy ELK layout failed, using grid fallback:', error);
		const cols = Math.ceil(Math.sqrt(nodes.length));
		return {
			nodes: nodes.map((node, i) => ({
				...node,
				position: { x: (i % cols) * 160, y: Math.floor(i / cols) * 50 }
			})),
			edges
		};
	}
}
