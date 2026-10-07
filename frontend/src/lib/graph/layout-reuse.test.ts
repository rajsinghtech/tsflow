import { describe, expect, it } from 'vitest';
import type { Edge, Node } from '@xyflow/svelte';
import { keepGroupViewport, maxReusableChurn, refitAfterLayout, rememberLayout, tryReuseLayout, type LayoutMemory } from './layout-reuse';

function flowNode(id: string, x: number, y: number): Node {
	return {
		id,
		type: 'network',
		position: { x, y },
		width: 200,
		height: 96,
		data: { displayName: id, label: id, ips: ['10.0.0.1'], totalBytes: 0, connections: 1 }
	};
}

function edge(source: string, target: string): Edge {
	return { id: `${source}-${target}`, source, target };
}

function memoryFrom(nodes: Node[]): LayoutMemory {
	const memory: LayoutMemory = { boxes: new Map() };
	rememberLayout(memory, nodes);
	return memory;
}

describe('layout reuse', () => {
	it('allows a small node-set change and refuses a large one', () => {
		expect(maxReusableChurn(0)).toBe(0);
		expect(maxReusableChurn(30)).toBe(2);
		expect(maxReusableChurn(1000)).toBe(40);
		expect(maxReusableChurn(5000)).toBe(40);
	});

	it('keeps coordinates when the edges change and the nodes do not', () => {
		const nodes = [flowNode('a', 10, 20), flowNode('b', 400, 20)];
		const memory = memoryFrom(nodes);
		const reused = tryReuseLayout(nodes, [edge('a', 'b')], memory, 150);
		expect(reused?.nodes.map((node) => [node.id, node.position])).toEqual([
			['a', { x: 10, y: 20 }],
			['b', { x: 400, y: 20 }]
		]);
		expect(reused?.edges).toHaveLength(1);
	});

	it('places one new node beside its neighbor without moving the others', () => {
		const nodes = [flowNode('a', 10, 20), flowNode('b', 400, 80)];
		const memory = memoryFrom(nodes);
		const added = flowNode('c', 0, 0);
		const reused = tryReuseLayout([...nodes, added], [edge('b', 'c')], memory, 150);
		expect(reused).not.toBeNull();
		const byId = new Map(reused!.nodes.map((node) => [node.id, node]));
		expect(byId.get('a')?.position).toEqual({ x: 10, y: 20 });
		expect(byId.get('b')?.position).toEqual({ x: 400, y: 80 });
		const created = byId.get('c')!;
		expect(created.position.x).toBeGreaterThanOrEqual(400 + 200);
		expect(created.position.y).toBe(80);
		const box = {
			x: created.position.x,
			y: created.position.y,
			width: created.width as number,
			height: created.height as number
		};
		for (const other of ['a', 'b']) {
			const node = byId.get(other)!;
			const previous = {
				x: node.position.x,
				y: node.position.y,
				width: node.width as number,
				height: node.height as number
			};
			const separated =
				box.x >= previous.x + previous.width ||
				previous.x >= box.x + box.width ||
				box.y >= previous.y + previous.height ||
				previous.y >= box.y + box.height;
			expect(separated).toBe(true);
		}
	});

	it('refuses to reuse when most of the node set is new', () => {
		const nodes = [flowNode('a', 0, 0), flowNode('b', 300, 0)];
		const memory = memoryFrom(nodes);
		const fresh = ['c', 'd', 'e', 'f'].map((id, index) => flowNode(id, 0, index));
		expect(tryReuseLayout(fresh, [], memory, 150)).toBeNull();
	});
});

describe('viewport after a layout', () => {
	it('fits a fresh layout that replaces a picture, and keeps the view for reused positions', () => {
		expect(refitAfterLayout({ hadPicture: true, reused: false, focusing: false })).toBe(true);
		expect(refitAfterLayout({ hadPicture: true, reused: true, focusing: false })).toBe(false);
		// The canvas fits the first picture when it mounts.
		expect(refitAfterLayout({ hadPicture: false, reused: false, focusing: false })).toBe(false);
		// A search focus moves the view itself.
		expect(refitAfterLayout({ hadPicture: true, reused: false, focusing: true })).toBe(false);
	});

	it('keeps the grouped viewport only between collapsed renders', () => {
		expect(keepGroupViewport({ reused: true, collapsedNow: true, collapsedBefore: true })).toBe(true);
		// After an expanded render the viewport fits that picture, so refit.
		expect(keepGroupViewport({ reused: true, collapsedNow: true, collapsedBefore: false })).toBe(false);
		expect(keepGroupViewport({ reused: false, collapsedNow: true, collapsedBefore: true })).toBe(false);
		expect(keepGroupViewport({ reused: true, collapsedNow: false, collapsedBefore: true })).toBe(false);
	});
});
