import { writable, derived, get } from 'svelte/store';
import { tailscaleService } from '#lib/services/tailscale-service';
import { dataSourceStore, queryTimeWindow } from './data-source-store';
import { createLiveRefresh } from './live-refresh';
import { lastUpdated } from './network-store';
import { DEFAULT_REFRESH_MS } from '#lib/utils/poll-interval';
import { filterStore } from './filter-store';
import type { TrafficStatsSummary, TrafficStatsBucket, PortStat } from '#lib/types';

interface StatsState {
	summary: TrafficStatsSummary | null;
	buckets: TrafficStatsBucket[];
	isLoading: boolean;
	error: string | null;
}

const defaultState: StatsState = {
	summary: null,
	buckets: [],
	isLoading: false,
	error: null
};

const statsState = writable<StatsState>(defaultState);

const statsLive = createLiveRefresh(() => {
	void loadStats(0);
});
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
		if (get(dataSourceStore).followLatest) {
			await dataSourceStore.fetchDataRange(signal);
			if (signal.aborted) return;
		}
		const { start, end } = get(queryTimeWindow);
		const trafficTypes = get(filterStore).trafficTypes;

		if (trafficTypes.length === 0) {
			statsState.set({
				summary: {
					tcpBytes: 0,
					udpBytes: 0,
					otherProtoBytes: 0,
					virtualBytes: 0,
					exitBytes: 0,
					subnetBytes: 0,
					physicalBytes: 0,
					totalFlows: 0,
					uniquePairs: 0,
					totalNodes: 0
				},
				buckets: [],
				isLoading: false,
				error: null
			});
			lastUpdated.set(new Date());
			clearStatsRetryState();
			return;
		}

		// Talkers and pairs are the ranked tables (rankings-store), not part of
		// this overview load.
		const overviewRes = await tailscaleService.getStatsOverview(start, end, signal, trafficTypes);

		if (signal.aborted) return;

		statsState.set({
			summary: overviewRes.summary,
			buckets: overviewRes.buckets || [],
			isLoading: false,
			error: null
		});
		lastUpdated.set(new Date());
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

export function startStatsRefresh(intervalMs = DEFAULT_REFRESH_MS) {
	statsLive.start(intervalMs);
	void loadStats();
}

export function clearStatsData() {
	if (statsController) {
		statsController.abort();
		statsController = null;
	}
	clearStatsRetryState();
	statsState.set(defaultState);
}

export function stopStatsRefresh() {
	statsLive.stop();
	if (statsController) {
		statsController.abort();
		statsController = null;
	}
	clearStatsRetryState();
}

export const statsSummary = derived(statsState, ($s) => $s.summary);
export const statsBuckets = derived(statsState, ($s) => $s.buckets);
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
		.sort((a, b) => b.bytes - a.bytes || a.proto - b.proto || a.port - b.port)
		.slice(0, 15);
});
