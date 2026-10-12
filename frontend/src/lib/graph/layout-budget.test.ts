import { describe, expect, it, vi } from 'vitest';
import type { ElkNode } from 'elkjs/lib/elk.bundled.js';
import { layoutWithinBudget, usesNetworkSimplex, withFastPlacement } from './layout-budget';

function graph(placement: string): ElkNode {
	return {
		id: 'root',
		layoutOptions: { 'elk.layered.nodePlacement.strategy': placement, 'elk.edgeRouting': 'SPLINES' },
		children: [{ id: 'a', width: 10, height: 10 }],
		edges: []
	};
}

describe('layoutWithinBudget', () => {
	it('keeps the quality layout when it finishes in time', async () => {
		const done = { id: 'root', children: [{ id: 'a', x: 1, y: 2 }] } as ElkNode;
		const attempt = { layout: vi.fn(async () => done), terminateWorker: vi.fn() };
		const fast = { layout: vi.fn() };
		const out = await layoutWithinBudget(graph('NETWORK_SIMPLEX'), { attempt: () => attempt, fast }, 1000);
		expect(out).toEqual({ result: done, quality: true });
		expect(fast.layout).not.toHaveBeenCalled();
		expect(attempt.terminateWorker).toHaveBeenCalledOnce();
	});

	it('stops a slow quality layout and uses the fast placement', async () => {
		vi.spyOn(console, 'warn').mockImplementation(() => {});
		const attempt = { layout: vi.fn(() => new Promise<ElkNode>(() => {})), terminateWorker: vi.fn() };
		const fastResult = { id: 'root', children: [] } as ElkNode;
		const fast = { layout: vi.fn(async (input: ElkNode) => ({ ...fastResult, layoutOptions: input.layoutOptions })) };
		const out = await layoutWithinBudget(graph('NETWORK_SIMPLEX'), { attempt: () => attempt, fast }, 20);
		expect(out.quality).toBe(false);
		expect(attempt.terminateWorker).toHaveBeenCalledOnce();
		const used = fast.layout.mock.calls[0][0] as ElkNode;
		expect(used.layoutOptions?.['elk.layered.nodePlacement.strategy']).toBe('BRANDES_KOEPF');
		expect(used.layoutOptions?.['elk.edgeRouting']).toBe('POLYLINE');
	});

	it('sends graphs that already use the fast placement straight to the shared worker', async () => {
		const attempt = vi.fn();
		const fast = { layout: vi.fn(async (input: ElkNode) => input) };
		const out = await layoutWithinBudget(graph('BRANDES_KOEPF'), { attempt, fast }, 20);
		expect(out.quality).toBe(false);
		expect(attempt).not.toHaveBeenCalled();
	});

	it('passes a quality layout error through and still stops the worker', async () => {
		const attempt = { layout: vi.fn(async () => Promise.reject(new Error('boom'))), terminateWorker: vi.fn() };
		await expect(layoutWithinBudget(graph('NETWORK_SIMPLEX'), { attempt: () => attempt, fast: { layout: vi.fn() } }, 1000)).rejects.toThrow('boom');
		expect(attempt.terminateWorker).toHaveBeenCalledOnce();
	});

	it('only swaps placement and routing', () => {
		const input = graph('NETWORK_SIMPLEX');
		input.layoutOptions!['elk.direction'] = 'DOWN';
		const fast = withFastPlacement(input);
		expect(fast.layoutOptions?.['elk.direction']).toBe('DOWN');
		expect(usesNetworkSimplex(fast)).toBe(false);
		expect(usesNetworkSimplex(input)).toBe(true);
	});
});
