/**
 * Graphs with this many nodes or fewer keep today's ELK layout and mount
 * every node. Above it, devices collapse into groups, those groups are
 * laid out with the same ELK pass, and only the cards inside the viewport
 * are mounted. Change this number to move the cutoff.
 */
export const FULL_GRAPH_NODE_THRESHOLD = 1200;

export function usesFullGraph(nodeCount: number): boolean {
	return nodeCount <= FULL_GRAPH_NODE_THRESHOLD;
}
