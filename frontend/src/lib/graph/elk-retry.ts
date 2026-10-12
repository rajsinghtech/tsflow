import type { ElkNode } from 'elkjs/lib/elk.bundled.js';

// Network-simplex placement recurses deeply on dense graphs. In a web worker,
// whose stack is smaller than the page's, it can throw "Maximum call stack
// size exceeded" on a graph of about 150 nodes and 1,000 edges. Running the
// same settings again on the main thread does finish, but it freezes the tab
// for a minute or two. The large-graph placement doesn't recurse like that, so
// a failed layout is retried with it, first in the worker and only then on the
// main thread.

export interface ElkEngine {
	layout(graph: ElkNode): Promise<ElkNode>;
}

// The graph with the placement and routing large graphs use, or null when it
// already uses them and a retry would just fail the same way.
export function fastRetryGraph(graph: ElkNode): ElkNode | null {
	const options = graph.layoutOptions ?? {};
	const slowPlacement = options['elk.layered.nodePlacement.strategy'] === 'NETWORK_SIMPLEX';
	const slowRouting = options['elk.edgeRouting'] === 'SPLINES';
	if (!slowPlacement && !slowRouting) return null;
	return {
		...graph,
		layoutOptions: {
			...options,
			...(slowPlacement ? { 'elk.layered.nodePlacement.strategy': 'BRANDES_KOEPF' } : {}),
			...(slowRouting ? { 'elk.edgeRouting': 'POLYLINE' } : {})
		}
	};
}

export interface RetriedLayout {
	result: ElkNode;
	// False when the original settings failed and the large-graph ones were used.
	original: boolean;
}

// Lays the graph out in the worker. When that fails, retries in the worker with
// the large-graph settings. When the worker can't lay out even those (it
// failed to start, say), the main thread does it, with the large-graph settings
// when there are any. Without a main-thread engine the error is passed on.
export async function layoutWithRetry(
	graph: ElkNode,
	worker: ElkEngine,
	mainThread?: () => ElkEngine
): Promise<RetriedLayout> {
	try {
		return { result: await worker.layout(graph), original: true };
	} catch (error) {
		const fast = fastRetryGraph(graph);
		if (fast) {
			console.warn('ELK layout failed in the worker, retrying with the large-graph placement:', error);
			try {
				return { result: await worker.layout(fast), original: false };
			} catch (retryError) {
				if (!mainThread) throw retryError;
				console.error('ELK worker failed, running ELK on the main thread:', retryError);
				return { result: await mainThread().layout(fast), original: false };
			}
		}
		if (!mainThread) throw error;
		console.error('ELK worker failed, running ELK on the main thread:', error);
		return { result: await mainThread().layout(graph), original: true };
	}
}
