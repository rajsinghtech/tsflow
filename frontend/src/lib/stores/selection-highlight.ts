import { derived } from 'svelte/store';
import { filteredEdges } from './network-store';
import { uiStore } from './ui-store';

// Kept out of ui-store so that store does not import the network graph.
// The graph store already imports ui-store.

export const highlightedNodeIds = derived([uiStore, filteredEdges], ([$ui, $edges]) => {
	const highlighted = new Set<string>();

	if ($ui.selectedNodeId) {
		highlighted.add($ui.selectedNodeId);
		$edges.forEach((edge) => {
			if (edge.source === $ui.selectedNodeId || edge.target === $ui.selectedNodeId) {
				highlighted.add(edge.source);
				highlighted.add(edge.target);
			}
		});
	} else if ($ui.selectedEdgeId) {
		const selectedEdge = $edges.find((edge) => edge.id === $ui.selectedEdgeId);
		if (selectedEdge) {
			highlighted.add(selectedEdge.source);
			highlighted.add(selectedEdge.target);
		}
	}

	return highlighted;
});

export const highlightedEdgeIds = derived([uiStore, filteredEdges], ([$ui, $edges]) => {
	const highlighted = new Set<string>();

	if ($ui.selectedNodeId) {
		$edges.forEach((edge) => {
			if (edge.source === $ui.selectedNodeId || edge.target === $ui.selectedNodeId) {
				highlighted.add(edge.id);
			}
		});
	} else if ($ui.selectedEdgeId) {
		highlighted.add($ui.selectedEdgeId);
	}

	return highlighted;
});
