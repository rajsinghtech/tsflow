import { writable, derived } from 'svelte/store';
import type { FilterState, Protocol, TrafficType } from '$lib/types';

const DEFAULT_TRAFFIC_TYPES: TrafficType[] = ['virtual', 'subnet'];
const ALL_TRAFFIC_TYPES = new Set<TrafficType>(['virtual', 'subnet', 'exit', 'physical']);

// LocalStorage key for persisted filter preferences. v2 intentionally drops
// older traffic-type preferences so the default is virtual + subnet.
const FILTER_STORAGE_KEY = 'tsflow-filter-prefs-v2';

function loadPersistedFilters(): Partial<FilterState> {
	if (typeof window === 'undefined') return {};
	try {
		const stored = localStorage.getItem(FILTER_STORAGE_KEY);
		if (!stored) return {};
		const parsed = JSON.parse(stored);
		// Only restore preferences that make sense to persist.
		// For trafficTypes: only override defaults if the stored value is a non-empty array.
		// An empty array means all types were deselected — restore defaults instead
		// so users don't get stuck with a permanently blank graph after reload.
		const result: Partial<FilterState> = {
			showIpv4: parsed.showIpv4 ?? true,
			showIpv6: parsed.showIpv6 ?? true
		};
		if (Array.isArray(parsed.trafficTypes) && parsed.trafficTypes.length > 0) {
			const trafficTypes = parsed.trafficTypes.filter((type: unknown): type is TrafficType =>
				typeof type === 'string' && ALL_TRAFFIC_TYPES.has(type as TrafficType)
			);
			if (trafficTypes.length > 0) {
				result.trafficTypes = trafficTypes;
			}
		}
		return result;
	} catch {
		return {};
	}
}

function persistFilters(state: FilterState) {
	if (typeof window === 'undefined') return;
	try {
		localStorage.setItem(FILTER_STORAGE_KEY, JSON.stringify({
			trafficTypes: state.trafficTypes,
			showIpv4: state.showIpv4,
			showIpv6: state.showIpv6
		}));
	} catch { /* quota exceeded or private browsing */ }
}

// Default filter state — virtual + subnet shown by default
const baseFilterState: FilterState = {
	search: '',
	protocols: [],
	trafficTypes: DEFAULT_TRAFFIC_TYPES,
	minBandwidth: 0,
	maxBandwidth: 1000000000, // 1GB
	minConnections: 0,
	showIpv4: true,
	showIpv6: true,
	selectedTags: []
};

const defaultFilterState: FilterState = {
	...baseFilterState,
	...loadPersistedFilters()
};

// Debounce delay for search filtering (ms)
const SEARCH_DEBOUNCE_MS = 300;

// Debounced search value - used by derived stores for filtering
const debouncedSearchStore = writable('');
let searchDebounceTimer: ReturnType<typeof setTimeout> | null = null;

// Create filter store with debounced search
function createFilterStore() {
	const { subscribe, set, update } = writable<FilterState>(defaultFilterState);

	return {
		subscribe,
		set,
		update,
		setSearch: (search: string) => {
			// Update immediate value for UI responsiveness
			update((s) => ({ ...s, search }));

			// Debounce the value used for expensive filtering operations
			if (searchDebounceTimer) clearTimeout(searchDebounceTimer);
			searchDebounceTimer = setTimeout(() => {
				debouncedSearchStore.set(search);
				searchDebounceTimer = null;
			}, SEARCH_DEBOUNCE_MS);
		},
		setProtocols: (protocols: Protocol[]) => update((s) => ({ ...s, protocols })),
		toggleProtocol: (protocol: Protocol) =>
			update((s) => ({
				...s,
				protocols: s.protocols.includes(protocol)
					? s.protocols.filter((p) => p !== protocol)
					: [...s.protocols, protocol]
			})),
		setTrafficTypes: (trafficTypes: TrafficType[]) => update((s) => {
			const next = { ...s, trafficTypes };
			persistFilters(next);
			return next;
		}),
		toggleTrafficType: (type: TrafficType) =>
			update((s) => {
				const next = {
					...s,
					trafficTypes: s.trafficTypes.includes(type)
						? s.trafficTypes.filter((t) => t !== type)
						: [...s.trafficTypes, type]
				};
				persistFilters(next);
				return next;
			}),
		setBandwidthRange: (min: number, max: number) =>
			update((s) => ({ ...s, minBandwidth: min, maxBandwidth: max })),
		setMinConnections: (min: number) => update((s) => ({ ...s, minConnections: min })),
		toggleIpv4: () => update((s) => {
			const next = { ...s, showIpv4: !s.showIpv4 };
			persistFilters(next);
			return next;
		}),
		toggleIpv6: () => update((s) => {
			const next = { ...s, showIpv6: !s.showIpv6 };
			persistFilters(next);
			return next;
		}),
		setSelectedTags: (tags: string[]) => update((s) => ({ ...s, selectedTags: tags })),
		reset: () => {
			set(baseFilterState);
			persistFilters(baseFilterState);
			// Also reset debounced search immediately
			if (searchDebounceTimer) {
				clearTimeout(searchDebounceTimer);
				searchDebounceTimer = null;
			}
			debouncedSearchStore.set('');
		},
		// Cleanup function to clear any pending timers (call on unmount)
		cleanup: () => {
			if (searchDebounceTimer) {
				clearTimeout(searchDebounceTimer);
				searchDebounceTimer = null;
			}
		}
	};
}

export const filterStore = createFilterStore();

// Derived store that combines filter state with debounced search
// Use this for expensive filtering operations (network-store, LogViewer)
export const debouncedFilterStore = derived(
	[filterStore, debouncedSearchStore],
	([$filters, $debouncedSearch]) => ({
		...$filters,
		search: $debouncedSearch
	})
);
