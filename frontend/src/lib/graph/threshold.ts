/**
 * Graphs with this many nodes or fewer keep the ELK layout and mount every
 * node. Above it, devices collapse into groups and only the viewport is
 * mounted. ELK refuses the same count, so changing this number moves both
 * cutoffs together.
 */
export const FULL_GRAPH_NODE_THRESHOLD = 1200;

export function usesFullGraph(nodeCount: number): boolean {
	return nodeCount <= FULL_GRAPH_NODE_THRESHOLD;
}
