import type { Edge, Node } from '@xyflow/svelte';
import { calculateNodeDimensions } from './elk-input';

export interface LayoutBox {
	x: number;
	y: number;
	width: number;
	height: number;
}

export interface LayoutMemory {
	boxes: Map<string, LayoutBox>;
}

// A refresh may add or drop a handful of devices. Past this, the layered
// picture would be wrong if we kept the old coordinates, so ELK runs again.
export function maxReusableChurn(previousCount: number): number {
	if (previousCount <= 0) return 0;
	return Math.max(2, Math.min(40, Math.round(previousCount * 0.05)));
}

function overlaps(a: LayoutBox, b: LayoutBox): boolean {
	return a.x < b.x + b.width && a.x + a.width > b.x && a.y < b.y + b.height && a.y + a.height > b.y;
}

function placeAdded(added: Node[], edges: Edge[], placed: Map<string, LayoutBox>, spacing: number) {
	const neighbors = new Map<string, string[]>();
	for (const edge of edges) {
		const from = neighbors.get(edge.source);
		if (from) from.push(edge.target);
		else neighbors.set(edge.source, [edge.target]);
		const to = neighbors.get(edge.target);
		if (to) to.push(edge.source);
		else neighbors.set(edge.target, [edge.source]);
	}

	const boxes = [...placed.values()];
	for (const node of [...added].sort((left, right) => left.id.localeCompare(right.id))) {
		const dimensions = calculateNodeDimensions(node);
		const anchors = (neighbors.get(node.id) ?? [])
			.map((id) => placed.get(id))
			.filter((box): box is LayoutBox => !!box);
		let x = 0;
		let y = 0;
		if (anchors.length > 0) {
			const anchor = anchors.reduce((best, box) => (box.x > best.x ? box : best));
			x = anchor.x + anchor.width + spacing;
			y = anchor.y;
		} else if (boxes.length > 0) {
			x = Math.max(...boxes.map((box) => box.x + box.width)) + spacing;
			y = Math.min(...boxes.map((box) => box.y));
		}
		const candidate = { x, y, width: dimensions.width, height: dimensions.height };
		let guard = 0;
		while (guard < 80 && boxes.some((box) => overlaps(candidate, box))) {
			guard += 1;
			candidate.y += dimensions.height + spacing;
		}
		if (boxes.some((box) => overlaps(candidate, box))) {
			candidate.x = (boxes.length > 0 ? Math.max(...boxes.map((box) => box.x + box.width)) : 0) + spacing;
			candidate.y = boxes.length > 0 ? Math.min(...boxes.map((box) => box.y)) : 0;
		}
		boxes.push(candidate);
		placed.set(node.id, candidate);
	}
}

// Keep coordinates when the node set barely changed. Returns null when the
// caller should run ELK. Does not mutate `memory`; the caller stores the
// layout it actually commits.
export function tryReuseLayout(
	nodes: Node[],
	edges: Edge[],
	memory: LayoutMemory,
	spacing = 150
): { nodes: Node[]; edges: Edge[] } | null {
	if (memory.boxes.size === 0 || nodes.length === 0) return null;
	const nextIds = new Set(nodes.map((node) => node.id));
	let removed = 0;
	for (const id of memory.boxes.keys()) {
		if (!nextIds.has(id)) removed += 1;
	}
	const added = nodes.filter((node) => !memory.boxes.has(node.id));
	const churn = added.length + removed;
	if (churn > maxReusableChurn(memory.boxes.size)) return null;

	const placed = new Map<string, LayoutBox>();
	for (const node of nodes) {
		const previous = memory.boxes.get(node.id);
		if (previous) placed.set(node.id, { ...previous });
	}
	placeAdded(added, edges, placed, spacing);

	return {
		nodes: nodes.map((node) => {
			const box = placed.get(node.id)!;
			return {
				...node,
				position: { x: box.x, y: box.y },
				width: box.width,
				height: box.height
			};
		}),
		edges
	};
}

export function rememberLayout(memory: LayoutMemory, nodes: Node[]) {
	memory.boxes.clear();
	for (const node of nodes) {
		memory.boxes.set(node.id, {
			x: node.position?.x ?? 0,
			y: node.position?.y ?? 0,
			width: (node.width as number) || 0,
			height: (node.height as number) || 0
		});
	}
}

// refitAfterLayout says whether the full graph should fit the view after new
// positions arrive. The previous picture stays up during a layout, so a fresh
// layout of a different graph would otherwise keep the old viewport and crop
// it. Reused positions keep the viewer's pan and zoom. A search focus moves the
// view itself. The first picture is fitted when the canvas mounts.
export function refitAfterLayout(input: { hadPicture: boolean; reused: boolean; focusing: boolean }): boolean {
	return input.hadPicture && !input.reused && !input.focusing;
}

// keepGroupViewport says whether the grouped view keeps the current viewport.
// Only a reused collapsed layout that follows a collapsed render keeps it. After
// an expanded render, the viewport fits the expanded picture, not this one.
export function keepGroupViewport(input: { reused: boolean; collapsedNow: boolean; collapsedBefore: boolean }): boolean {
	return input.reused && input.collapsedNow && input.collapsedBefore;
}
