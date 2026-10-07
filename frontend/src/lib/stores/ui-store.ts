import { writable, derived } from 'svelte/store';
import type { UIState } from '#lib/types';

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

// Check if there's any selection
export const hasSelection = derived(uiStore, ($ui) => $ui.selectedNodeId !== null || $ui.selectedEdgeId !== null);
