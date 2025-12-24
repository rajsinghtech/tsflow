import { writable } from 'svelte/store';
import axios from 'axios';
import type { Device, NetworkLog, NetworkNode, NetworkEdge, ServicesResponse } from '$lib/types';

// Error store for global error handling
export const errorStore = writable<string | null>(null);

// Devices store
function createDevicesStore() {
	const { subscribe, set, update } = writable<Device[]>([]);

	return {
		subscribe,
		fetchDevices: async () => {
			try {
				const response = await axios.get('/api/devices');
				set(response.data.devices || []);
				errorStore.set(null); // Clear any previous errors
			} catch (error) {
				console.error('Failed to fetch devices:', error);
				const errorMessage = error instanceof Error ? error.message : 'Failed to fetch devices';
				errorStore.set(errorMessage);
				// Don't re-throw - let caller handle via error store
			}
		},
		reset: () => set([])
	};
}

// Network logs store with metadata
interface NetworkLogsMetadata {
	chunked?: boolean;
	chunks?: number;
	duration?: string;
	totalLogs?: number;
	sampled?: boolean;
	sampleRate?: number;
	lastUpdated?: string;
}

function createNetworkLogsStore() {
	const logsStore = writable<NetworkLog[]>([]);
	const metadataStore = writable<NetworkLogsMetadata>({});

	return {
		subscribe: logsStore.subscribe,
		metadata: { subscribe: metadataStore.subscribe },
		fetchLogs: async (minutesOrStart?: number | string, endDate?: string) => {
			try {
				let start: Date;
				let end: Date;

				if (typeof minutesOrStart === 'string' && endDate) {
					// Custom date range
					start = new Date(minutesOrStart);
					end = new Date(endDate);
				} else {
					// Minutes from now
					const minutes = typeof minutesOrStart === 'number' ? minutesOrStart : 5;
					end = new Date();
					start = new Date(end.getTime() - minutes * 60 * 1000);
				}

				const response = await axios.get('/api/network-logs', {
					params: {
						start: start.toISOString(),
						end: end.toISOString()
					}
				});

				logsStore.set(response.data.logs || []);
				metadataStore.set({
					...(response.data.metadata || {}),
					lastUpdated: new Date().toISOString()
				});
				errorStore.set(null); // Clear any previous errors
			} catch (error) {
				console.error('Failed to fetch network logs:', error);
				const errorMessage = error instanceof Error ? error.message : 'Failed to fetch network logs';
				errorStore.set(errorMessage);
				// Don't re-throw - let caller handle via error store
			}
		},
		reset: () => {
			logsStore.set([]);
			metadataStore.set({});
		}
	};
}

// Filters store
function createFiltersStore() {
	const { subscribe, set, update } = writable({
		searchQuery: '',
		protocols: ['TCP', 'UDP', 'ICMP', 'Proto-0'],
		trafficTypes: ['virtual', 'subnet', 'physical'], // Exit node traffic excluded by default (too cluttered)
		ipCategories: ['tailscale', 'private', 'public'], // derp excluded by default (like production)
		timeRange: 5
	});

	return {
		subscribe,
		updateSearch: (query: string) => {
			// Trim and sanitize search query to avoid issues with whitespace
			const sanitized = query?.trim() || '';
			update(s => ({ ...s, searchQuery: sanitized }));
		},
		updateProtocols: (protocols: string[]) => update(s => ({ ...s, protocols })),
		updateTrafficTypes: (types: string[]) => update(s => ({ ...s, trafficTypes: types })),
		updateIpCategories: (categories: string[]) => update(s => ({ ...s, ipCategories: categories })),
		updateTimeRange: (minutes: number) => update(s => ({ ...s, timeRange: minutes })),
		reset: () => set({
			searchQuery: '',
			protocols: ['TCP', 'UDP', 'ICMP', 'Proto-0'],
			trafficTypes: ['virtual', 'subnet', 'physical'], // Exit node traffic excluded by default
			ipCategories: ['tailscale', 'private', 'public'], // derp excluded by default (like production)
			timeRange: 5
		})
	};
}

// Network overview store for header stats
interface NetworkOverview {
	nodes: NetworkNode[];
	links: NetworkEdge[];
	totalTraffic: number;
}

function createNetworkOverviewStore() {
	const { subscribe, set, update } = writable<NetworkOverview>({
		nodes: [],
		links: [],
		totalTraffic: 0
	});

	return {
		subscribe,
		updateOverview: (nodes: NetworkNode[], links: NetworkEdge[]) => {
			const totalTraffic = nodes.reduce((sum, node) => {
				// Calculate total bytes from node if it has that property
				const nodeBytes = (node as any).totalBytes || 0;
				return sum + nodeBytes;
			}, 0);
			set({ nodes, links, totalTraffic });
		},
		reset: () => set({ nodes: [], links: [], totalTraffic: 0 })
	};
}

// Services and static records store
function createServicesStore() {
	const { subscribe, set } = writable<ServicesResponse>({
		services: {},
		records: {}
	});

	return {
		subscribe,
		fetchServices: async () => {
			try {
				const response = await axios.get('/api/services-records');
				set({
					services: response.data.services || {},
					records: response.data.records || {}
				});
				errorStore.set(null);
			} catch (error) {
				console.error('Failed to fetch services:', error);
				const errorMessage = error instanceof Error ? error.message : 'Failed to fetch services';
				errorStore.set(errorMessage);
			}
		},
		reset: () => set({ services: {}, records: {} })
	};
}

export const devicesStore = createDevicesStore();
export const networkLogsStore = createNetworkLogsStore();
export const filtersStore = createFiltersStore();
export const networkOverviewStore = createNetworkOverviewStore();
export const servicesStore = createServicesStore();
