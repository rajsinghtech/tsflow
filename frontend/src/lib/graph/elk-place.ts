import type { ElkExtendedEdge, ElkNode } from 'elkjs/lib/elk.bundled.js';
import type { Edge, Node } from '@xyflow/svelte';
import type { NetworkNode } from '#lib/types';
import type { RenderEdge, RenderModel, RenderNode } from './aggregate';
import { calculateNodeDimensions, layeredLayoutOptions } from './elk-input';
import { edgeStyle } from './full-graph';

export const GROUP_LAYOUT_OPTIONS = { algorithm: 'layered' as const, nodeSpacing: 90 };

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
	devices: Map<string, NetworkNode>,
	matched: ReadonlySet<string> = new Set()
): { nodes: Node[]; edges: Edge[] } {
	return {
		nodes: model.nodes.map((node) => {
			if (node.kind === 'device' && node.deviceId) {
				const device = devices.get(node.deviceId);
				if (device) {
					const flow = deviceFlowNode(device);
					if (matched.has(node.deviceId)) {
						flow.data = { ...flow.data, searchMatch: true };
					}
					return flow;
				}
			}
			const flow = groupFlowNode(node);
			if (node.memberIds.some((id) => matched.has(id))) {
				flow.data = { ...flow.data, searchMatch: true };
			}
			return flow;
		}),
		edges: flowEdges(model.edges)
	};
}

function elkChild(node: Node): ElkNode {
	const dimensions = calculateNodeDimensions(node);
	return { id: node.id, width: dimensions.width, height: dimensions.height };
}

function endpoint(id: string, members: ReadonlySet<string>, groupId: string): string {
	return members.has(id) ? groupId : id;
}

// One INCLUDE_CHILDREN run. Members live inside the opened group. Edges that
// leave the group attach to the group itself, so they meet the group boundary
// instead of fanning from every member. Other cards are siblings and get
// pushed aside by the same layout.
export function buildCompoundGraph(
	model: RenderModel,
	devices: Map<string, NetworkNode>,
	groupId: string,
	memberIds: readonly string[]
): ElkNode {
	const members = new Set(memberIds);
	const rootOptions = layeredLayoutOptions(GROUP_LAYOUT_OPTIONS);
	rootOptions['elk.hierarchyHandling'] = 'INCLUDE_CHILDREN';

	const children: ElkNode[] = [];
	for (const node of model.nodes) {
		if (members.has(node.id)) continue;
		const flow =
			node.kind === 'device' && node.deviceId && devices.get(node.deviceId)
				? deviceFlowNode(devices.get(node.deviceId)!)
				: groupFlowNode(node);
		children.push(elkChild(flow));
	}

	const memberNodes = memberIds
		.map((id) => devices.get(id))
		.filter((device): device is NetworkNode => !!device)
		.map((device) => elkChild(deviceFlowNode(device)));

	const internal: ElkExtendedEdge[] = [];
	const external = new Map<string, RenderEdge>();
	for (const edge of model.edges) {
		const sourceIn = members.has(edge.source);
		const targetIn = members.has(edge.target);
		if (sourceIn && targetIn) {
			internal.push({ id: edge.id, sources: [edge.source], targets: [edge.target] });
			continue;
		}
		const source = endpoint(edge.source, members, groupId);
		const target = endpoint(edge.target, members, groupId);
		if (source === target) continue;
		const [left, right] = source < target ? [source, target] : [target, source];
		const id = `${left}<->${right}|${edge.trafficType}`;
		const existing = external.get(id);
		if (existing) {
			existing.totalBytes += edge.totalBytes;
			continue;
		}
		external.set(id, { ...edge, id, source: left, target: right });
	}

	children.push({
		id: groupId,
		layoutOptions: {
			'elk.algorithm': 'layered',
			'elk.hierarchyHandling': 'INCLUDE_CHILDREN',
			'elk.direction': 'RIGHT',
			'elk.padding': '[top=44,left=20,bottom=20,right=20]',
			'elk.spacing.nodeNode': '20',
			'elk.layered.spacing.nodeNodeBetweenLayers': '36',
			'elk.edgeRouting': 'SPLINES'
		},
		children: memberNodes,
		edges: internal
	});

	return {
		id: 'root',
		layoutOptions: rootOptions,
		children,
		edges: [...external.values()].map((edge) => ({
			id: edge.id,
			sources: [edge.source],
			targets: [edge.target]
		}))
	};
}

export function boxesOverlap(
	a: { x: number; y: number; width: number; height: number },
	b: { x: number; y: number; width: number; height: number }
): boolean {
	return a.x < b.x + b.width && a.x + a.width > b.x && a.y < b.y + b.height && a.y + a.height > b.y;
}

export function sceneFromCompound(
	layout: ElkNode,
	groupId: string,
	home: LayoutBox,
	model: RenderModel,
	devices: Map<string, NetworkNode>,
	label: { displayName: string; groupKind?: string; memberCount: number }
): { nodes: Node[]; edges: Edge[] } {
	const parent = layout.children?.find((child) => child.id === groupId);
	const dx = parent ? home.x + home.width / 2 - ((parent.x ?? 0) + (parent.width ?? 0) / 2) : 0;
	const dy = parent ? home.y + home.height / 2 - ((parent.y ?? 0) + (parent.height ?? 0) / 2) : 0;
	const byModel = new Map(model.nodes.map((node) => [node.id, node]));
	const nodes: Node[] = [];

	for (const child of layout.children ?? []) {
		const moved = { x: (child.x ?? 0) + dx, y: (child.y ?? 0) + dy };
		if (child.id === groupId) {
			nodes.push({
				id: groupId,
				type: 'cluster',
				position: moved,
				width: child.width,
				height: child.height,
				style: `width: ${child.width ?? 0}px; height: ${child.height ?? 0}px;`,
				data: {
					id: groupId,
					label: label.displayName,
					displayName: label.displayName,
					kind: 'cluster',
					groupKind: label.groupKind,
					memberCount: label.memberCount,
					totalBytes: 0,
					connections: 0
				}
			});
			for (const member of child.children ?? []) {
				const device = devices.get(member.id!);
				if (!device) continue;
				nodes.push({
					...deviceFlowNode(device),
					parentId: groupId,
					extent: 'parent' as const,
					position: { x: member.x ?? 0, y: member.y ?? 0 },
					width: member.width,
					height: member.height
				});
			}
			continue;
		}
		const render = byModel.get(child.id!);
		const flow = render
			? render.kind === 'device' && render.deviceId && devices.get(render.deviceId)
				? deviceFlowNode(devices.get(render.deviceId)!)
				: groupFlowNode(render)
			: { id: child.id!, type: 'group', position: { x: 0, y: 0 }, data: { label: child.id } };
		nodes.push({
			...flow,
			position: moved,
			width: child.width,
			height: child.height
		});
	}

	const memberIds = new Set((parent?.children ?? []).map((child) => child.id!));
	const edges = flowEdges(boundaryEdges(model.edges, memberIds, groupId));
	nodes.sort((left, right) => Number(!!left.parentId) - Number(!!right.parentId));
	return { nodes, edges };
}

export function boundaryEdges(
	edges: RenderEdge[],
	memberIds: ReadonlySet<string>,
	groupId: string
): RenderEdge[] {
	const merged = new Map<string, RenderEdge>();
	for (const edge of edges) {
		const sourceIn = memberIds.has(edge.source);
		const targetIn = memberIds.has(edge.target);
		if (sourceIn && targetIn) {
			merged.set(edge.id, edge);
			continue;
		}
		const source = sourceIn ? groupId : edge.source;
		const target = targetIn ? groupId : edge.target;
		if (source === target) continue;
		const [left, right] = source < target ? [source, target] : [target, source];
		const id = `${left}<->${right}|${edge.trafficType}`;
		const existing = merged.get(id);
		if (existing && !memberIds.has(existing.source)) {
			existing.totalBytes += edge.totalBytes;
			continue;
		}
		merged.set(id, { ...edge, id, source: left, target: right });
	}
	return [...merged.values()];
}
