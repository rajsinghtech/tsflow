import { writable, derived } from 'svelte/store';
import type { UIState } from '#lib/types';
import { filteredEdges } from './network-store';

const defaultUIState: UIState = {
	showFilterPanel: true,
	showLogViewer: true,
	mobileDrawerOpen: false,
	selectedNodeId: null,
	selectedEdgeId: null,
	isLoading: false,
	error: null
};

function createUIStore() {
	const { subscribe, set, update } = writable<UIState>(defaultUIState);

	return {
		subscribe,
		toggleFilterPanel: () => update((s) => ({ ...s, showFilterPanel: !s.showFilterPanel })),
		toggleLogViewer: () => update((s) => ({ ...s, showLogViewer: !s.showLogViewer })),
		openMobileDrawer: () => update((s) => ({ ...s, mobileDrawerOpen: true })),
		closeMobileDrawer: () => update((s) => ({ ...s, mobileDrawerOpen: false })),
		toggleMobileDrawer: () => update((s) => ({ ...s, mobileDrawerOpen: !s.mobileDrawerOpen })),
		// Unified filter toggle: uses desktop sidebar on lg+, mobile drawer below
		toggleFilters: () => {
			if (typeof window !== 'undefined' && window.innerWidth >= 1024) {
				update((s) => ({ ...s, showFilterPanel: !s.showFilterPanel }));
			} else {
				update((s) => ({ ...s, mobileDrawerOpen: !s.mobileDrawerOpen }));
			}
		},
		selectNode: (nodeId: string | null) =>
			update((s) => ({ ...s, selectedNodeId: nodeId, selectedEdgeId: null })),
		selectEdge: (edgeId: string | null) =>
			update((s) => ({ ...s, selectedEdgeId: edgeId, selectedNodeId: null })),
		clearSelection: () => update((s) => ({ ...s, selectedNodeId: null, selectedEdgeId: null })),
		setLoading: (loading: boolean) => update((s) => ({ ...s, isLoading: loading })),
		setError: (error: string | null) => update((s) => ({ ...s, error })),
		reset: () => set(defaultUIState)
	};
}

export const uiStore = createUIStore();

// Derived store for highlighted nodes based on selection
export const highlightedNodeIds = derived(
	[uiStore, filteredEdges],
	([$ui, $edges]) => {
		const highlighted = new Set<string>();

		if ($ui.selectedNodeId) {
			// Highlight selected node and all connected nodes
			highlighted.add($ui.selectedNodeId);
			$edges.forEach((edge) => {
				if (edge.source === $ui.selectedNodeId || edge.target === $ui.selectedNodeId) {
					highlighted.add(edge.source);
					highlighted.add(edge.target);
				}
			});
		} else if ($ui.selectedEdgeId) {
			// Highlight nodes connected by selected edge
			const selectedEdge = $edges.find((e) => e.id === $ui.selectedEdgeId);
			if (selectedEdge) {
				highlighted.add(selectedEdge.source);
				highlighted.add(selectedEdge.target);
			}
		}

		return highlighted;
	}
);

// Check if there's any selection
export const hasSelection = derived(uiStore, ($ui) => $ui.selectedNodeId !== null || $ui.selectedEdgeId !== null);

// Derived store for highlighted edges based on selection
export const highlightedEdgeIds = derived(
	[uiStore, filteredEdges],
	([$ui, $edges]) => {
		const highlighted = new Set<string>();

		if ($ui.selectedNodeId) {
			// Highlight edges connected to selected node
			$edges.forEach((edge) => {
				if (edge.source === $ui.selectedNodeId || edge.target === $ui.selectedNodeId) {
					highlighted.add(edge.id);
				}
			});
		} else if ($ui.selectedEdgeId) {
			// Highlight the selected edge
			highlighted.add($ui.selectedEdgeId);
		}

		return highlighted;
	}
);
