import { describe, expect, it } from 'vitest';
import ELK, { type ElkNode } from 'elkjs/lib/elk.bundled.js';
import { buildRenderModel } from './aggregate';
import { buildElkLayoutInput } from './elk-input';
import { GROUP_LAYOUT_OPTIONS, modelToFlow } from './elk-place';
import { toFlowElements } from './full-graph';
import { rememberLayout, tryReuseLayout, type LayoutMemory } from './layout-reuse';
import { realisticTailnet } from './synthetic-tailnet';
import { usesFullGraph } from './threshold';

const elk = new ELK();

function clone<T>(value: T): T {
	return JSON.parse(JSON.stringify(value)) as T;
}

function legacyGraph(graph: ElkNode): ElkNode {
	return {
		...graph,
		layoutOptions: {
			...graph.layoutOptions,
			'elk.edgeRouting': 'SPLINES',
			'elk.layered.nodePlacement.strategy': 'NETWORK_SIMPLEX'
		}
	};
}

async function timeLayout(graph: ElkNode): Promise<{ ms: number; result: ElkNode }> {
	const started = performance.now();
	const result = await elk.layout(clone(graph));
	return { ms: performance.now() - started, result };
}

function layerCount(result: ElkNode): number {
	const bands = new Set((result.children ?? []).map((child) => Math.round((child.y ?? 0) / 40)));
	return bands.size;
}

describe('layout timing', () => {
	it('reports layered layout time before and after for 500, 1000, and 5000 nodes', async () => {
		const lines: string[] = [];
		for (const count of [500, 1000]) {
			const data = realisticTailnet(count);
			expect(usesFullGraph(data.nodes.length)).toBe(true);
			const flow = toFlowElements(data.nodes, data.edges);
			const after = buildElkLayoutInput(flow.nodes, flow.edges, { algorithm: 'layered', nodeSpacing: 150 });
			const before = await timeLayout(legacyGraph(after));
			const afterRun = await timeLayout(after);
			lines.push(
				`full ${count} nodes=${data.nodes.length} edges=${data.edges.length} before=${before.ms.toFixed(0)}ms after=${afterRun.ms.toFixed(0)}ms layers=${layerCount(afterRun.result)}`
			);
			expect(afterRun.ms).toBeLessThan(before.ms);
			expect(layerCount(afterRun.result)).toBeGreaterThan(2);
			expect(layerCount(afterRun.result)).toBeLessThan(40);
		}

		const large = realisticTailnet(5000);
		expect(usesFullGraph(large.nodes.length)).toBe(false);
		const model = buildRenderModel(large.nodes, large.edges, new Set());
		const devices = new Map(large.nodes.map((node) => [node.id, node]));
		const grouped = modelToFlow(model, devices);
		const groupedInput = buildElkLayoutInput(grouped.nodes, grouped.edges, GROUP_LAYOUT_OPTIONS);
		const groupedBefore = await timeLayout(legacyGraph(groupedInput));
		const groupedAfter = await timeLayout(groupedInput);
		lines.push(
			`grouped 5000 devices=${large.nodes.length} groups=${model.groupCount} renderNodes=${model.nodes.length} renderEdges=${model.edges.length} before=${groupedBefore.ms.toFixed(0)}ms after=${groupedAfter.ms.toFixed(0)}ms`
		);
		expect(groupedAfter.ms).toBeLessThan(2000);
		expect(groupedBefore.ms).toBeLessThan(2000);

		const fullFlow = toFlowElements(large.nodes, large.edges);
		const fullAfter = buildElkLayoutInput(fullFlow.nodes, fullFlow.edges, { algorithm: 'layered', nodeSpacing: 150 });
		const fullAfterRun = await timeLayout(fullAfter);
		lines.push(
			`full 5000 nodes=${large.nodes.length} edges=${large.edges.length} after=${fullAfterRun.ms.toFixed(0)}ms layers=${layerCount(fullAfterRun.result)} (the app uses the grouped path; simplex on this graph is not run here)`
		);
		expect(fullAfter.layoutOptions?.['elk.layered.nodePlacement.strategy']).toBe('BRANDES_KOEPF');

		const refreshGraph = realisticTailnet(1000);
		const refreshFlow = toFlowElements(refreshGraph.nodes, refreshGraph.edges);
		const refreshInput = buildElkLayoutInput(refreshFlow.nodes, refreshFlow.edges, {
			algorithm: 'layered',
			nodeSpacing: 150
		});
		const laid = await elk.layout(clone(refreshInput));
		const placed = refreshFlow.nodes.map((node) => {
			const child = laid.children?.find((item) => item.id === node.id);
			return { ...node, position: { x: child?.x ?? 0, y: child?.y ?? 0 }, width: child?.width, height: child?.height };
		});
		const memory: LayoutMemory = { boxes: new Map() };
		rememberLayout(memory, placed);
		const extra = { ...refreshFlow.nodes[0], id: 'refresh-extra' };
		const started = performance.now();
		const reused = tryReuseLayout([...refreshFlow.nodes, extra], refreshFlow.edges, memory, 150);
		const refreshMs = performance.now() - started;
		lines.push(`refresh 1000+1 reuse=${refreshMs.toFixed(2)}ms`);
		expect(reused).not.toBeNull();
		expect(refreshMs).toBeLessThan(50);

		console.log(lines.join('\n'));
	}, 180000);
});
