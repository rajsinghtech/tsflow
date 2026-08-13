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
	let dataRangeGeneration = 0;
	let dataRangeRequestToken = 0;
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

		async fetchDataRange(signal?: AbortSignal) {
			if (signal?.aborted) return null;
			// Requests with a caller-owned signal must not share the uncancellable
			// legacy request or another caller's signal. This keeps a refresh that
			// was superseded from updating the active refresh's state.
			if (!signal && dataRangeRequest) return dataRangeRequest;
			if (signal) dataRangeRequest = null;
			const requestToken = ++dataRangeRequestToken;
			const requestGeneration = dataRangeGeneration;
			update((s) => ({ ...s, isLoading: true, error: null }));
			let request: Promise<DataRange | null> | null = null;
			request = (async () => {
				try {
					const dataRange = await tailscaleService.getDataRange(signal);
					if (signal?.aborted || requestToken !== dataRangeRequestToken || requestGeneration !== dataRangeGeneration) return null;
					update((s) => {
						const next = { ...s, dataRange };
						if (s.followLatest && hasValidRange(dataRange)) {
							const selected = latestWindow(dataRange, s.latestWindowMs);
							next.selectedStart = selected.start;
							next.selectedEnd = selected.end;
						}
						return next;
					});
					return dataRange;
				} catch (err) {
					if (signal?.aborted || requestToken !== dataRangeRequestToken || requestGeneration !== dataRangeGeneration) return null;
					const error = err instanceof Error ? err.message : 'Failed to fetch data range';
					update((s) => ({ ...s, error }));
					return null;
				} finally {
					if (requestToken === dataRangeRequestToken && requestGeneration === dataRangeGeneration) {
						update((s) => ({ ...s, isLoading: false }));
					}
					if (!signal && request !== null && dataRangeRequest === request) dataRangeRequest = null;
				}
			})();
			if (!signal) dataRangeRequest = request;
			return request;
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
			dataRangeGeneration++;
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
