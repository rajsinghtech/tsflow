import type { NetworkLink, NetworkNode, TrafficType } from '#lib/types';
import { layoutItems, type LayoutPosition } from './cheap-layout';

export const GROUP_NODE_WIDTH = 220;
export const GROUP_NODE_HEIGHT = 108;
export const DEVICE_NODE_WIDTH = 220;
export const DEVICE_NODE_HEIGHT = 120;
export const LARGE_LAYOUT_SPACING = 72;
export const MAX_MOUNTED_NODES = 300;

const MIN_GROUP_SIZE = 2;

export type GroupKind = 'tag' | 'user' | 'subnet';

export interface GroupKey {
	kind: GroupKind;
	key: string;
	label: string;
}

export interface RenderNode {
	id: string;
	kind: 'device' | 'group';
	label: string;
	sublabel: string;
	width: number;
	height: number;
	totalBytes: number;
	connections: number;
	memberCount: number;
	memberIds: string[];
	groupKind?: GroupKind;
	deviceId?: string;
}

export interface RenderEdge {
	id: string;
	source: string;
	target: string;
	totalBytes: number;
	txBytes: number;
	rxBytes: number;
	trafficType: TrafficType;
}

export interface RenderModel {
	nodes: RenderNode[];
	edges: RenderEdge[];
	deviceCount: number;
	groupCount: number;
}

export interface PlacedNode extends RenderNode {
	x: number;
	y: number;
}

export interface WorldViewport {
	x: number;
	y: number;
	width: number;
	height: number;
}

export function groupIdFor(key: GroupKey): string {
	return `group:${key.kind}:${key.key}`;
}

export function tagKey(node: NetworkNode): GroupKey | undefined {
	let best: string | undefined;
	for (const tag of node.tags ?? []) {
		if (!tag.startsWith('tag:')) continue;
		const name = tag.slice(4);
		if (!name) continue;
		if (!best || name < best) best = name;
	}
	if (!best) return undefined;
	return { kind: 'tag', key: best, label: `tag:${best}` };
}

export function userKey(node: NetworkNode): GroupKey | undefined {
	const user = node.user?.trim();
	if (!user) return undefined;
	return { kind: 'user', key: user, label: user };
}

export function subnetLabel(ip: string): string {
	if (!ip) return 'unknown';
	if (ip.includes(':')) {
		const head: string[] = [];
		for (const part of ip.split(':')) {
			if (head.length === 4) break;
			if (part === '') break;
			head.push(part.toLowerCase());
		}
		while (head.length < 4) head.push('0');
		return `${head.join(':')}::/64`;
	}
	const parts = ip.split('.');
	if (parts.length === 4 && parts.every((part) => /^\d+$/.test(part))) {
		return `${parts[0]}.${parts[1]}.${parts[2]}.0/24`;
	}
	return 'unknown';
}

export function subnetKey(node: NetworkNode): GroupKey {
	const label = subnetLabel(node.ip || node.ips?.[0] || '');
	return { kind: 'subnet', key: label, label };
}

function deviceNode(node: NetworkNode): RenderNode {
	const ip = node.ip || node.ips?.[0] || '';
	const sublabel = [node.user, ip].filter(Boolean).join(' · ');
	return {
		id: node.id,
		kind: 'device',
		label: node.displayName || node.id,
		sublabel,
		width: DEVICE_NODE_WIDTH,
		height: DEVICE_NODE_HEIGHT,
		totalBytes: node.totalBytes,
		connections: node.connections,
		memberCount: 1,
		memberIds: [node.id],
		deviceId: node.id
	};
}

function takeGroups(
	input: NetworkNode[],
	keyOf: (node: NetworkNode) => GroupKey | undefined,
	expanded: ReadonlySet<string>
): { groups: RenderNode[]; devices: NetworkNode[]; remainder: NetworkNode[] } {
	const buckets = new Map<string, NetworkNode[]>();
	const keys = new Map<string, GroupKey>();
	const remainder: NetworkNode[] = [];

	for (const node of input) {
		const key = keyOf(node);
		if (!key) {
			remainder.push(node);
			continue;
		}
		const id = groupIdFor(key);
		keys.set(id, key);
		const list = buckets.get(id);
		if (list) list.push(node);
		else buckets.set(id, [node]);
	}

	const groups: RenderNode[] = [];
	const devices: NetworkNode[] = [];
	for (const [id, members] of buckets) {
		if (members.length < MIN_GROUP_SIZE) {
			remainder.push(...members);
			continue;
		}
		if (expanded.has(id)) {
			devices.push(...members);
			continue;
		}
		const key = keys.get(id)!;
		let totalBytes = 0;
		let connections = 0;
		const memberIds = new Array<string>(members.length);
		for (let index = 0; index < members.length; index++) {
			totalBytes += members[index].totalBytes;
			connections += members[index].connections;
			memberIds[index] = members[index].id;
		}
		memberIds.sort();
		groups.push({
			id,
			kind: 'group',
			label: key.label,
			sublabel: `${members.length.toLocaleString()} devices`,
			width: GROUP_NODE_WIDTH,
			height: GROUP_NODE_HEIGHT,
			totalBytes,
			connections,
			memberCount: members.length,
			memberIds,
			groupKind: key.kind
		});
	}

	return { groups, devices, remainder };
}

function aggregateEdges(edges: NetworkLink[], representative: Map<string, string>): RenderEdge[] {
	const merged = new Map<string, RenderEdge>();
	for (const edge of edges) {
		const source = representative.get(edge.source);
		const target = representative.get(edge.target);
		if (!source || !target || source === target) continue;
		const [left, right] = source < target ? [source, target] : [target, source];
		const id = `${left}<->${right}|${edge.trafficType}`;
		const existing = merged.get(id);
		if (existing) {
			existing.totalBytes += edge.totalBytes;
			existing.txBytes += edge.txBytes;
			existing.rxBytes += edge.rxBytes;
			continue;
		}
		merged.set(id, {
			id,
			source: left,
			target: right,
			totalBytes: edge.totalBytes,
			txBytes: edge.txBytes,
			rxBytes: edge.rxBytes,
			trafficType: edge.trafficType
		});
	}
	return [...merged.values()];
}

// Collapse by tag, then user, then subnet. A bucket of one stays a device.
// Expanded groups come back as their member devices and are not regrouped.
export function buildRenderModel(
	nodes: NetworkNode[],
	edges: NetworkLink[],
	expanded: ReadonlySet<string>
): RenderModel {
	const byTag = takeGroups(nodes, tagKey, expanded);
	const byUser = takeGroups(byTag.remainder, userKey, expanded);
	const bySubnet = takeGroups(byUser.remainder, subnetKey, expanded);

	const renderNodes = [
		...byTag.groups,
		...byUser.groups,
		...bySubnet.groups,
		...byTag.devices.map(deviceNode),
		...byUser.devices.map(deviceNode),
		...bySubnet.devices.map(deviceNode),
		...bySubnet.remainder.map(deviceNode)
	];
	renderNodes.sort((left, right) => {
		if (left.kind !== right.kind) return left.kind === 'group' ? -1 : 1;
		const byLabel = left.label.localeCompare(right.label, undefined, { numeric: true });
		return byLabel || left.id.localeCompare(right.id);
	});

	const representative = new Map<string, string>();
	for (const node of renderNodes) {
		if (node.kind !== 'group') continue;
		for (const memberId of node.memberIds) representative.set(memberId, node.id);
	}
	for (const node of renderNodes) {
		if (node.kind === 'device' && node.deviceId && !representative.has(node.deviceId)) {
			representative.set(node.deviceId, node.id);
		}
	}

	const groupCount = renderNodes.reduce((count, node) => count + (node.kind === 'group' ? 1 : 0), 0);
	return {
		nodes: renderNodes,
		edges: aggregateEdges(edges, representative),
		deviceCount: nodes.length,
		groupCount
	};
}

export function placeNodes(nodes: RenderNode[], positions: LayoutPosition[]): PlacedNode[] {
	const byId = new Map(positions.map((position) => [position.id, position]));
	return nodes.map((node) => {
		const position = byId.get(node.id);
		return { ...node, x: position?.x ?? 0, y: position?.y ?? 0 };
	});
}

export function placeModel(model: RenderModel, spacing = LARGE_LAYOUT_SPACING): PlacedNode[] {
	const positions = layoutItems(
		model.nodes.map((node) => ({ id: node.id, width: node.width, height: node.height })),
		spacing
	);
	return placeNodes(model.nodes, positions);
}

export function boundsOf(nodes: Array<{ x: number; y: number; width: number; height: number }>) {
	let minX = Infinity;
	let minY = Infinity;
	let maxX = -Infinity;
	let maxY = -Infinity;
	for (const node of nodes) {
		minX = Math.min(minX, node.x);
		minY = Math.min(minY, node.y);
		maxX = Math.max(maxX, node.x + node.width);
		maxY = Math.max(maxY, node.y + node.height);
	}
	if (!Number.isFinite(minX)) return { x: 0, y: 0, width: 1, height: 1 };
	return { x: minX, y: minY, width: Math.max(1, maxX - minX), height: Math.max(1, maxY - minY) };
}

function distanceToCenter(node: PlacedNode, centerX: number, centerY: number): number {
	const dx = node.x + node.width / 2 - centerX;
	const dy = node.y + node.height / 2 - centerY;
	return dx * dx + dy * dy;
}

export function cullToViewport(
	nodes: PlacedNode[],
	edges: RenderEdge[],
	viewport: WorldViewport,
	limit = MAX_MOUNTED_NODES,
	overscan = 80
): { nodes: PlacedNode[]; edges: RenderEdge[] } {
	const left = viewport.x - overscan;
	const top = viewport.y - overscan;
	const right = viewport.x + viewport.width + overscan;
	const bottom = viewport.y + viewport.height + overscan;
	const hits: PlacedNode[] = [];
	for (const node of nodes) {
		if (node.x + node.width < left || node.x > right || node.y + node.height < top || node.y > bottom) {
			continue;
		}
		hits.push(node);
	}

	let kept = hits;
	if (hits.length > limit) {
		const centerX = viewport.x + viewport.width / 2;
		const centerY = viewport.y + viewport.height / 2;
		kept = hits
			.slice()
			.sort((leftNode, rightNode) => distanceToCenter(leftNode, centerX, centerY) - distanceToCenter(rightNode, centerX, centerY))
			.slice(0, limit);
	}

	const ids = new Set(kept.map((node) => node.id));
	return {
		nodes: kept,
		edges: edges.filter((edge) => ids.has(edge.source) && ids.has(edge.target))
	};
}
