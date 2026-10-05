import type { Node, Edge } from '@xyflow/svelte';
import type { NetworkLink, NetworkNode, TrafficType } from '#lib/types';

export function edgeStyle(
	edge: { trafficType: TrafficType; totalBytes: number },
	dimmed = false
): string {
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

// Nodes and edges the graph passes to ELK at or below the full-graph threshold.
export function toFlowElements(
	nodes: NetworkNode[],
	edges: NetworkLink[]
): { nodes: Node[]; edges: Edge[] } {
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
			style: edgeStyle(edge)
		}))
	};
}
