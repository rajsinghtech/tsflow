import { get, writable } from 'svelte/store';
import { dataSourceStore } from './data-source-store';
import { loadNetworkData } from './network-store';
import { loadStats } from './stats-store';
import { loadRankings } from './rankings-store';

export function pinWindow(): void {
	const state = get(dataSourceStore);
	if (!state.followLatest || !state.selectedStart || !state.selectedEnd) return;
	dataSourceStore.setSelectedRange(state.selectedStart, state.selectedEnd);
}

export function resumeLive(): void {
	const state = get(dataSourceStore);
	dataSourceStore.showLatestWindow(state.dataRange, state.latestWindowMs);
}

// Pages that load their own data (New, Me) reload when this counter moves.
// Traffic, Analytics and Policy are handled directly below.
export const pageRefresh = writable(0);

export async function refreshVisibleData(): Promise<void> {
	if (typeof window === 'undefined') return;
	const path = window.location.pathname;
	if (path === '/analytics' || path.startsWith('/analytics/')) {
		// A new window returns the ranked tables to their first page.
		await Promise.all([loadStats(), loadRankings(true)]);
		return;
	}
	if (path === '/policy' || path.startsWith('/policy/')) return;
	if (path === '/') {
		await loadNetworkData();
		return;
	}
	pageRefresh.update((n) => n + 1);
}

export async function toggleLive(): Promise<void> {
	if (get(dataSourceStore).followLatest) {
		pinWindow();
		return;
	}
	resumeLive();
	await refreshVisibleData();
}
