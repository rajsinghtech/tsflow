// Active device totals come from the overview count of distinct nodes in the
// window. The top-talkers ranking is capped, so its length is not a population.

export function resolveActiveNodeCount(totalNodes: number | null | undefined): number {
	if (typeof totalNodes !== 'number' || !Number.isFinite(totalNodes) || totalNodes < 0) return 0;
	return Math.trunc(totalNodes);
}

// Traffic view uses the graph, which already includes every node in that
// window. Once overview stats are loaded, other pages use that distinct count
// instead of the capped talker list. Before they load, a graph count is still
// a real population.
export function headerNodeCount(
	networkNodes: number,
	totalNodes: number | null | undefined,
	statsLoaded: boolean,
	useNetwork: boolean
): number {
	if (useNetwork && networkNodes > 0) return networkNodes;
	if (statsLoaded) return resolveActiveNodeCount(totalNodes);
	if (networkNodes > 0) return networkNodes;
	return 0;
}

export function averageBytesPerNode(totalBytes: number, nodeCount: number): number {
	if (!Number.isFinite(totalBytes) || totalBytes <= 0 || nodeCount <= 0) return 0;
	return totalBytes / nodeCount;
}
