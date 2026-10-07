import { get } from 'svelte/store';
import { dataSourceStore } from './data-source-store';
import { loadNetworkData } from './network-store';
import { loadStats } from './stats-store';

export function pinWindow(): void {
	const state = get(dataSourceStore);
	if (!state.followLatest || !state.selectedStart || !state.selectedEnd) return;
	dataSourceStore.setSelectedRange(state.selectedStart, state.selectedEnd);
}

export function resumeLive(): void {
	const state = get(dataSourceStore);
	dataSourceStore.showLatestWindow(state.dataRange, state.latestWindowMs);
}

export async function refreshVisibleData(): Promise<void> {
	if (typeof window === 'undefined') return;
	const path = window.location.pathname;
	if (path === '/analytics' || path.startsWith('/analytics/')) {
		await loadStats();
		return;
	}
	if (path === '/policy' || path.startsWith('/policy/')) return;
	await loadNetworkData();
}

export async function toggleLive(): Promise<void> {
	if (get(dataSourceStore).followLatest) {
		pinWindow();
		return;
	}
	resumeLive();
	await refreshVisibleData();
}
