import { writable, derived, get } from 'svelte/store';
import { tailscaleService } from '$lib/services/tailscale-service';
import { dataSourceStore } from './data-source-store';
import { timeRangeStore, TIME_RANGES } from './filter-store';
import type { TrafficStatsSummary, TrafficStatsBucket, TopTalker, TopPair, PortStat } from '$lib/types';

interface StatsState {
	summary: TrafficStatsSummary | null;
	buckets: TrafficStatsBucket[];
	topTalkers: TopTalker[];
	topPairs: TopPair[];
	isLoading: boolean;
	error: string | null;
}

const defaultState: StatsState = {
	summary: null,
	buckets: [],
	topTalkers: [],
	topPairs: [],
	isLoading: false,
	error: null
};

const statsState = writable<StatsState>(defaultState);

let refreshTimer: ReturnType<typeof setInterval> | null = null;
let statsController: AbortController | null = null;

// Retry state
const MAX_RETRIES = 3;
export const statsRetryCount = writable(0);
export const statsRetryingIn = writable<number | null>(null);
let statsRetryTimeout: ReturnType<typeof setTimeout> | null = null;
let statsRetryTickInterval: ReturnType<typeof setInterval> | null = null;

function clearStatsRetryState() {
	if (statsRetryTimeout) {
		clearTimeout(statsRetryTimeout);
		statsRetryTimeout = null;
	}
	if (statsRetryTickInterval) {
		clearInterval(statsRetryTickInterval);
		statsRetryTickInterval = null;
	}
	statsRetryCount.set(0);
	statsRetryingIn.set(null);
}

function scheduleStatsRetry(attempt: number) {
	if (statsRetryTickInterval) {
		clearInterval(statsRetryTickInterval);
	}

	const delaySec = Math.pow(2, attempt - 1);
	statsRetryingIn.set(delaySec);

	let remaining = delaySec;
	statsRetryTickInterval = setInterval(() => {
		remaining--;
		if (remaining > 0) {
			statsRetryingIn.set(remaining);
		} else {
			if (statsRetryTickInterval) {
				clearInterval(statsRetryTickInterval);
				statsRetryTickInterval = null;
			}
		}
	}, 1000);

	statsRetryTimeout = setTimeout(() => {
		statsRetryingIn.set(null);
		loadStats(attempt);
	}, delaySec * 1000);
}

export async function loadStats(currentAttempt = 0) {
	if (statsController) {
		statsController.abort();
	}
	statsController = new AbortController();
	const signal = statsController.signal;

	statsState.update((s) => ({ ...s, isLoading: true, error: null }));

	try {
		// Compute time window fresh each call to avoid stale derived store values.
		let start: Date;
		let end: Date;

		let ds = get(dataSourceStore);

		if (ds.mode === 'historical' && ds.selectedStart && ds.selectedEnd) {
			start = ds.selectedStart;
			end = ds.selectedEnd;
		} else {
			// Live mode - use time range store (same as network data)
			const timeRange = get(timeRangeStore);
			if (timeRange.selected === 'custom' && timeRange.customStart && timeRange.customEnd) {
				start = timeRange.customStart;
				end = timeRange.customEnd;
			} else {
				const preset = TIME_RANGES.find((p) => p.value === timeRange.selected);
				end = new Date();
				start = new Date(end.getTime() - (preset?.minutes || 5) * 60 * 1000);
			}
		}

		let [overviewRes, talkersRes, pairsRes] = await Promise.all([
			tailscaleService.getStatsOverview(start, end, signal),
			tailscaleService.getTopTalkers(start, end, 15, signal),
			tailscaleService.getTopPairs(start, end, 15, signal)
		]);

		if (signal.aborted) return;

		statsState.set({
			summary: overviewRes.summary,
			buckets: overviewRes.buckets || [],
			topTalkers: talkersRes.talkers || [],
			topPairs: pairsRes.pairs || [],
			isLoading: false,
			error: null
		});
		clearStatsRetryState();
	} catch (err) {
		if (signal.aborted) return;
		console.error('Failed to load stats:', err);
		statsState.update((s) => ({
			...s,
			isLoading: false,
			error: err instanceof Error ? err.message : 'Failed to load stats'
		}));

		const nextAttempt = currentAttempt + 1;
		statsRetryCount.set(nextAttempt);
		if (nextAttempt < MAX_RETRIES) {
			scheduleStatsRetry(nextAttempt);
		}
	}
}

export function retryLoadStats() {
	clearStatsRetryState();
	loadStats(0);
}

export function startStatsRefresh(intervalMs = 60_000) {
	stopStatsRefresh();
	loadStats();
	// Don't auto-refresh in historical mode - data is static
	if (get(dataSourceStore).mode === 'historical') return;
	refreshTimer = setInterval(() => loadStats(0), intervalMs);
}

export function stopStatsRefresh() {
	if (refreshTimer) {
		clearInterval(refreshTimer);
		refreshTimer = null;
	}
}

export const statsSummary = derived(statsState, ($s) => $s.summary);
export const statsBuckets = derived(statsState, ($s) => $s.buckets);
export const topTalkers = derived(statsState, ($s) => $s.topTalkers);
export const topPairs = derived(statsState, ($s) => $s.topPairs);
export const statsLoading = derived(statsState, ($s) => $s.isLoading);
export const statsError = derived(statsState, ($s) => $s.error);

export const topPorts = derived(statsState, ($s): PortStat[] => {
	if (!$s.buckets || $s.buckets.length === 0) return [];
	const portMap = new Map<string, PortStat>();
	for (const bucket of $s.buckets) {
		try {
			const ports: PortStat[] = JSON.parse(bucket.topPorts || '[]');
			for (const p of ports) {
				const key = `${p.proto}:${p.port}`;
				const existing = portMap.get(key);
				if (existing) {
					existing.bytes += p.bytes;
				} else {
					portMap.set(key, { ...p });
				}
			}
		} catch {
			// skip malformed JSON
		}
	}
	return Array.from(portMap.values())
		.sort((a, b) => b.bytes - a.bytes)
		.slice(0, 15);
});
