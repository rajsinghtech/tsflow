import { writable } from 'svelte/store';
import type { DeviceScope } from '#lib/types';
import { filterStore } from './filter-store';

export interface ViewerIdentity {
	login?: string;
	name?: string;
	node?: string;
	groups?: string[];
	tailnets?: string[];
	autoscope?: string;
	deviceScope?: DeviceScope | null;
}

export const viewerStore = writable<ViewerIdentity | null>(null);
// viewerReady is true after the whoami lookup finishes, including when
// there is no login. The traffic page waits for it before choosing a landing tab.
export const viewerReady = writable(false);

// loadViewerIdentity asks whoami once. No identity, an error, or
// autoscope off leaves the current filters alone.
export async function loadViewerIdentity(): Promise<void> {
	try {
		const response = await fetch('/api/whoami');
		if (!response.ok) return;
		const data = (await response.json()) as ViewerIdentity;
		if (data.login || data.name || data.node) {
			viewerStore.set(data);
		} else {
			viewerStore.set(null);
		}
		if (data.autoscope === 'user' || data.autoscope === 'groups') {
			filterStore.setDeviceScope({
				owners: data.deviceScope?.owners ?? [],
				tags: data.deviceScope?.tags ?? []
			});
		}
	} catch {
		// Identity is optional. A failed lookup keeps the unfiltered view.
	} finally {
		viewerReady.set(true);
	}
}
