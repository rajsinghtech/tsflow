// Tailnet query state for API calls. One tailnet leaves this unset so data
// URLs stay the same as a single-tailnet install.

let loader: (() => Promise<void>) | null = null;
let ready: Promise<void> | null = null;
let activeId: string | null = null;

export interface TailnetPollerStatus {
	running: boolean;
	lastPollTime?: string;
	pollErrors?: number;
	lastError?: string;
}

export interface TailnetInfo {
	id: string;
	displayName: string;
	poller: TailnetPollerStatus;
}

export function setTailnetLoader(fn: () => Promise<void>) {
	loader = fn;
}

export function activeTailnet(): string | null {
	return activeId;
}

export function setActiveTailnet(id: string | null) {
	activeId = id;
}

// The first data call waits for GET /api/tailnets. Later calls reuse that result.
export function ensureTailnetQuery(): Promise<void> {
	if (!ready) {
		ready = (async () => {
			if (!loader) {
				await import('#lib/stores/tailnet-store');
			}
			if (loader) await loader();
		})();
	}
	return ready;
}

export function withTailnet(endpoint: string): string {
	if (!activeId) return endpoint;
	const join = endpoint.includes('?') ? '&' : '?';
	return `${endpoint}${join}tailnet=${encodeURIComponent(activeId)}`;
}

export function tailnetParamFromSearch(search: string): string | null {
	const raw = search.startsWith('?') ? search.slice(1) : search;
	const value = new URLSearchParams(raw).get('tailnet')?.trim() ?? '';
	return value || null;
}

// With one tailnet the selection is omitted. With several, keep a valid URL
// value, otherwise id default, otherwise the first configured id.
export function chooseTailnet(list: TailnetInfo[], requested: string | null): string | null {
	if (list.length < 2) return null;
	if (requested && list.some((tailnet) => tailnet.id === requested)) return requested;
	if (list.some((tailnet) => tailnet.id === 'default')) return 'default';
	return list[0]?.id ?? null;
}

export function searchWithTailnet(currentSearch: string, id: string | null): string {
	const params = new URLSearchParams(currentSearch.startsWith('?') ? currentSearch.slice(1) : currentSearch);
	if (id) params.set('tailnet', id);
	else params.delete('tailnet');
	const next = params.toString();
	return next ? `?${next}` : '';
}

export function hrefWithTailnet(path: string, id: string | null): string {
	if (!id) return path;
	const hashAt = path.indexOf('#');
	const hash = hashAt >= 0 ? path.slice(hashAt) : '';
	const withoutHash = hashAt >= 0 ? path.slice(0, hashAt) : path;
	const queryAt = withoutHash.indexOf('?');
	const pathname = queryAt >= 0 ? withoutHash.slice(0, queryAt) : withoutHash;
	const search = queryAt >= 0 ? withoutHash.slice(queryAt) : '';
	return pathname + searchWithTailnet(search, id) + hash;
}

export function resetTailnetQueryForTests() {
	activeId = null;
	ready = null;
}
