import type { Edge, Node } from '@xyflow/svelte';
import type { NetworkNode } from '#lib/types';
import type { RenderEdge, RenderModel, RenderNode } from './aggregate';
import { edgeStyle } from './full-graph';

export const GROUP_LAYOUT_OPTIONS = { algorithm: 'layered' as const, nodeSpacing: 150 };

export interface LayoutBox {
	id: string;
	x: number;
	y: number;
	width: number;
	height: number;
}

export function groupFlowNode(node: RenderNode): Node {
	return {
		id: node.id,
		type: 'group',
		position: { x: 0, y: 0 },
		data: {
			id: node.id,
			label: node.label,
			displayName: node.label,
			totalBytes: node.totalBytes,
			connections: node.connections,
			user: `${node.memberCount} devices`,
			ips: [],
			tags: [],
			isTailscale: true,
			kind: 'group',
			groupKind: node.groupKind,
			memberCount: node.memberCount
		}
	};
}

export function deviceFlowNode(device: NetworkNode): Node {
	return {
		id: device.id,
		type: 'network',
		position: { x: 0, y: 0 },
		data: {
			label: device.displayName,
			...device
		}
	};
}

export function flowEdges(edges: RenderEdge[]): Edge[] {
	return edges.map((edge) => ({
		id: edge.id,
		source: edge.source,
		target: edge.target,
		type: 'default',
		style: edgeStyle(edge)
	}));
}

export function modelToFlow(
	model: RenderModel,
	devices: Map<string, NetworkNode>
): { nodes: Node[]; edges: Edge[] } {
	return {
		nodes: model.nodes.map((node) => {
			if (node.kind === 'device' && node.deviceId) {
				const device = devices.get(node.deviceId);
				if (device) return deviceFlowNode(device);
			}
			return groupFlowNode(node);
		}),
		edges: flowEdges(model.edges)
	};
}

// Members of the opened group, plus the neighboring cards those edges touch.
// The neighbors are anchors: ELK sees them so the member layout is edge-aware,
// and the caller puts them back where they already were.
export function expansionSubgraph(
	model: RenderModel,
	memberIds: ReadonlySet<string>,
	devices: Map<string, NetworkNode>,
	current: Map<string, Node>
): { nodes: Node[]; edges: Edge[]; anchorIds: string[] } {
	const needed = new Set<string>(memberIds);
	for (const edge of model.edges) {
		const sourceIn = memberIds.has(edge.source);
		const targetIn = memberIds.has(edge.target);
		if (sourceIn || targetIn) {
			needed.add(edge.source);
			needed.add(edge.target);
		}
	}

	const nodes: Node[] = [];
	const anchorIds: string[] = [];
	for (const id of needed) {
		if (memberIds.has(id)) {
			const device = devices.get(id);
			if (device) nodes.push(deviceFlowNode(device));
			continue;
		}
		const existing = current.get(id);
		if (!existing) continue;
		anchorIds.push(id);
		nodes.push({
			...existing,
			position: { x: 0, y: 0 }
		});
	}

	const edges = flowEdges(
		model.edges.filter((edge) => needed.has(edge.source) && needed.has(edge.target))
	);
	return { nodes, edges, anchorIds };
}

function center(boxes: Array<{ x: number; y: number; width: number; height: number }>) {
	let minX = Infinity;
	let minY = Infinity;
	let maxX = -Infinity;
	let maxY = -Infinity;
	for (const box of boxes) {
		minX = Math.min(minX, box.x);
		minY = Math.min(minY, box.y);
		maxX = Math.max(maxX, box.x + box.width);
		maxY = Math.max(maxY, box.y + box.height);
	}
	return { x: (minX + maxX) / 2, y: (minY + maxY) / 2 };
}

// Shift the member layout so existing cards stay put. Anchors keep their
// previous coordinates. Members move by the same delta as the anchor centroid,
// which is how a separate ELK run stays attached to the graph around it.
export function placeExpandedLayout(
	laidOut: LayoutBox[],
	kept: Map<string, { x: number; y: number }>,
	memberIds: ReadonlySet<string>,
	groupBox: { x: number; y: number; width: number; height: number } | null
): Map<string, { x: number; y: number }> {
	const next = new Map(kept);
	const members = laidOut.filter((node) => memberIds.has(node.id));
	const anchors = laidOut.filter((node) => kept.has(node.id) && !memberIds.has(node.id));

	if (anchors.length > 0) {
		let oldX = 0;
		let oldY = 0;
		let newX = 0;
		let newY = 0;
		for (const anchor of anchors) {
			const previous = kept.get(anchor.id)!;
			oldX += previous.x;
			oldY += previous.y;
			newX += anchor.x;
			newY += anchor.y;
		}
		const dx = oldX / anchors.length - newX / anchors.length;
		const dy = oldY / anchors.length - newY / anchors.length;
		for (const member of members) {
			next.set(member.id, { x: member.x + dx, y: member.y + dy });
		}
		return next;
	}

	if (groupBox && members.length > 0) {
		const from = center(members);
		const to = {
			x: groupBox.x + groupBox.width / 2,
			y: groupBox.y + groupBox.height / 2
		};
		const dx = to.x - from.x;
		const dy = to.y - from.y;
		for (const member of members) {
			next.set(member.id, { x: member.x + dx, y: member.y + dy });
		}
		return next;
	}

	for (const member of members) next.set(member.id, { x: member.x, y: member.y });
	return next;
}
