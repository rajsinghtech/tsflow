import type { ElkNode } from 'elkjs/lib/elk.bundled.js';

// Network-simplex placement is usually quick for graphs under the quality
// limits, but a dense graph (many edges spanning many layers) can keep it busy
// for minutes, and every later layout queues behind it. A quality layout gets
// this long in its own worker. Past it, the worker is stopped and the graph is
// laid out with the large-graph settings instead. Graphs that finish in time
// get exactly the layout they got before.
export const QUALITY_LAYOUT_BUDGET_MS = 6000;

export interface LayoutEngine {
	layout(graph: ElkNode): Promise<ElkNode>;
	terminateWorker?(): void;
}

export function usesNetworkSimplex(graph: ElkNode): boolean {
	return graph.layoutOptions?.['elk.layered.nodePlacement.strategy'] === 'NETWORK_SIMPLEX';
}

// The same graph with the placement and routing large graphs use.
export function withFastPlacement(graph: ElkNode): ElkNode {
	return {
		...graph,
		layoutOptions: {
			...graph.layoutOptions,
			'elk.layered.nodePlacement.strategy': 'BRANDES_KOEPF',
			'elk.edgeRouting': 'POLYLINE'
		}
	};
}

export interface BudgetedLayout {
	result: ElkNode;
	// False when the quality attempt ran out of time and the fast settings were used.
	quality: boolean;
}

export async function layoutWithinBudget(
	graph: ElkNode,
	engines: { attempt: () => LayoutEngine; fast: LayoutEngine },
	budgetMs = QUALITY_LAYOUT_BUDGET_MS
): Promise<BudgetedLayout> {
	if (!usesNetworkSimplex(graph)) {
		return { result: await engines.fast.layout(graph), quality: false };
	}
	const attempt = engines.attempt();
	let timer: ReturnType<typeof setTimeout> | undefined;
	const timedOut = new Promise<null>((resolve) => {
		timer = setTimeout(() => resolve(null), budgetMs);
	});
	try {
		const result = await Promise.race([attempt.layout(graph), timedOut]);
		if (result) return { result, quality: true };
	} finally {
		if (timer) clearTimeout(timer);
		attempt.terminateWorker?.();
	}
	console.warn(`Graph layout took over ${budgetMs} ms, switching to the faster large-graph placement`);
	return { result: await engines.fast.layout(withFastPlacement(graph)), quality: false };
}
