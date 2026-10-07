import { get, writable } from 'svelte/store';
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

// whenViewerReady resolves once whoami has answered, or after timeoutMs so a
// slow lookup cannot hold up the page that waits on it.
export function whenViewerReady(timeoutMs = 2000): Promise<void> {
	if (get(viewerReady)) return Promise.resolve();
	return new Promise((resolve) => {
		let unsubscribe: (() => void) | undefined;
		const finish = () => {
			clearTimeout(timer);
			unsubscribe?.();
			resolve();
		};
		const timer = setTimeout(finish, timeoutMs);
		unsubscribe = viewerReady.subscribe((ready) => {
			if (ready) queueMicrotask(finish);
		});
	});
}

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
