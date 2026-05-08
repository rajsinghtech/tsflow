import { writable, derived } from 'svelte/store';
import { tailscaleService, type DataRange, type PollerStatus } from '$lib/services';

const DEFAULT_WINDOW_MS = 2 * 60 * 60 * 1000;
const MIN_WINDOW_MS = 5 * 60 * 1000;

interface DataSourceState {
	dataRange: DataRange | null;
	pollerStatus: PollerStatus | null;
	selectedStart: Date | null;
	selectedEnd: Date | null;
	latestWindowMs: number;
	followLatest: boolean;
	isLoading: boolean;
	error: string | null;
}

const defaultState: DataSourceState = {
	dataRange: null,
	pollerStatus: null,
	selectedStart: null,
	selectedEnd: null,
	latestWindowMs: DEFAULT_WINDOW_MS,
	followLatest: true,
	isLoading: false,
	error: null
};

function hasValidRange(range: DataRange | null): range is DataRange {
	if (!range || !range.earliest || !range.latest || range.count === 0) return false;
	const earliest = new Date(range.earliest);
	const latest = new Date(range.latest);
	return earliest.getFullYear() > 1970 && latest > earliest;
}

function latestWindow(range: DataRange, windowMs = DEFAULT_WINDOW_MS): { start: Date; end: Date } {
	const earliest = new Date(range.earliest);
	const latest = new Date(range.latest);
	const boundedWindowMs = Math.min(windowMs, Math.max(MIN_WINDOW_MS, latest.getTime() - earliest.getTime()));
	return {
		start: new Date(latest.getTime() - boundedWindowMs),
		end: latest
	};
}

function createDataSourceStore() {
	const { subscribe, set, update } = writable<DataSourceState>(defaultState);
	let dataRangeRequest: Promise<DataRange | null> | null = null;
	let pollerStatusRequest: Promise<PollerStatus | null> | null = null;

	return {
		subscribe,

		setSelectedRange: (start: Date, end: Date) =>
			update((s) => ({
				...s,
				selectedStart: start,
				selectedEnd: end,
				latestWindowMs: Math.max(MIN_WINDOW_MS, end.getTime() - start.getTime()),
				followLatest: false
			})),

		showLatestWindow: (range?: DataRange | null, windowMs = DEFAULT_WINDOW_MS) =>
			update((s) => {
				const sourceRange = range ?? s.dataRange;
				const nextWindowMs = Math.max(MIN_WINDOW_MS, windowMs);
				if (!hasValidRange(sourceRange)) {
					return { ...s, latestWindowMs: nextWindowMs, followLatest: true };
				}
				const selected = latestWindow(sourceRange, nextWindowMs);
				return {
					...s,
					selectedStart: selected.start,
					selectedEnd: selected.end,
					latestWindowMs: nextWindowMs,
					followLatest: true
				};
			}),

		async fetchDataRange() {
			if (dataRangeRequest) return dataRangeRequest;
			update((s) => ({ ...s, isLoading: true, error: null }));
			dataRangeRequest = (async () => {
				try {
					const dataRange = await tailscaleService.getDataRange();
					update((s) => {
						const next = { ...s, dataRange, isLoading: false };
						if (s.followLatest && hasValidRange(dataRange)) {
							const selected = latestWindow(dataRange, s.latestWindowMs);
							next.selectedStart = selected.start;
							next.selectedEnd = selected.end;
						}
						return next;
					});
					return dataRange;
				} catch (err) {
					const error = err instanceof Error ? err.message : 'Failed to fetch data range';
					update((s) => ({ ...s, error, isLoading: false }));
					return null;
				} finally {
					dataRangeRequest = null;
				}
			})();
			return dataRangeRequest;
		},

		async fetchPollerStatus() {
			if (pollerStatusRequest) return pollerStatusRequest;
			pollerStatusRequest = (async () => {
				try {
					const pollerStatus = await tailscaleService.getPollerStatus();
					update((s) => ({ ...s, pollerStatus }));
					return pollerStatus;
				} catch (err) {
					const error = err instanceof Error ? err.message : 'Failed to fetch poller status';
					console.error('Failed to fetch poller status:', err);
					update((s) => ({ ...s, error }));
					return null;
				} finally {
					pollerStatusRequest = null;
				}
			})();
			return pollerStatusRequest;
		},

		reset: () => {
			dataRangeRequest = null;
			pollerStatusRequest = null;
			set(defaultState);
		}
	};
}

export const dataSourceStore = createDataSourceStore();

export const hasStoredData = derived(dataSourceStore, ($store) => hasValidRange($store.dataRange));

export const queryTimeWindow = derived(dataSourceStore, ($store) => {
	if ($store.selectedStart && $store.selectedEnd) {
		return {
			start: $store.selectedStart,
			end: $store.selectedEnd
		};
	}
	if (hasValidRange($store.dataRange)) {
		return latestWindow($store.dataRange);
	}
	const now = new Date();
	return {
		start: new Date(now.getTime() - 60 * 60 * 1000),
		end: now
	};
});
