import { pushState, replaceState } from '$app/navigation';
import { page } from '$app/state';
import { get } from 'svelte/store';
import {
	decodeSearch,
	encodeIntent,
	intentFromParts,
	rangeSignature,
	rememberRange,
	type RangeIntent,
	type TimeZoneMode
} from '#lib/components/timeline/time-range-url';
import { ALL_LIVE_MS, MIN_WINDOW_MS } from '#lib/components/timeline/time-window';
import { dataSourceStore } from './data-source-store';
import { refreshVisibleData } from './live-mode';

let applied = '';
let bootstrapped = false;

export function bootstrapRangeFromLocation() {
	if (bootstrapped || typeof window === 'undefined') return;
	bootstrapped = true;
	const decoded = decodeSearch(new URLSearchParams(window.location.search));
	if (decoded.status !== 'ok') return;
	applyIntent(decoded.intent);
	applied = rangeSignature(window.location.search);
}

export function syncRangeFromUrl(search: string) {
	const signature = rangeSignature(search);
	if (signature === applied) return;
	const decoded = decodeSearch(new URLSearchParams(search.startsWith('?') ? search.slice(1) : search));
	if (decoded.status === 'invalid') return;
	if (decoded.status === 'missing') {
		publishIntent(intentFromStore(), 'replace', false);
		return;
	}
	applied = signature;
	applyIntent(decoded.intent);
	void refreshVisibleData();
}

export function commitIntent(intent: RangeIntent, zone: TimeZoneMode = 'local') {
	publishIntent(intent, 'push', true);
	rememberRange(intent, zone);
}

export function pinCurrentRange() {
	const state = get(dataSourceStore);
	if (!state.followLatest || !state.selectedStart || !state.selectedEnd) return;
	publishIntent({ kind: 'absolute', start: state.selectedStart, end: state.selectedEnd }, 'push', false);
}

export function resumeSlidingLive() {
	const state = get(dataSourceStore);
	if (state.latestWindowMs >= 365 * 24 * 60 * 60 * 1000) {
		commitIntent({ kind: 'all' });
		return;
	}
	commitIntent({ kind: 'sliding', windowMs: state.latestWindowMs || MIN_WINDOW_MS });
}

export function shiftWindow(direction: -1 | 1) {
	const state = get(dataSourceStore);
	if (!state.selectedStart || !state.selectedEnd) return;
	const width = Math.max(MIN_WINDOW_MS, state.selectedEnd.getTime() - state.selectedStart.getTime());
	let start = state.selectedStart.getTime() + direction * width;
	let end = state.selectedEnd.getTime() + direction * width;
	const coverage = coverageBounds();
	if (coverage) {
		if (start < coverage.start) {
			end += coverage.start - start;
			start = coverage.start;
		}
		if (end > coverage.end) {
			start -= end - coverage.end;
			end = coverage.end;
		}
		start = Math.max(coverage.start, start);
		end = Math.min(coverage.end, end);
	}
	if (end - start < MIN_WINDOW_MS) return;
	if (state.selectedStart.getTime() === start && state.selectedEnd.getTime() === end && !state.followLatest) return;
	commitIntent({ kind: 'absolute', start: new Date(start), end: new Date(end) });
}

export function zoomWindow(factor: number) {
	const state = get(dataSourceStore);
	const year = 365 * 24 * 60 * 60 * 1000;
	if (state.followLatest && !state.anchoredStart) {
		if (state.latestWindowMs >= year && factor > 1) return;
		if (state.latestWindowMs >= year && factor < 1) {
			commitIntent({ kind: 'sliding', windowMs: 7 * 24 * 60 * 60 * 1000 });
			return;
		}
		commitIntent({ kind: 'sliding', windowMs: Math.max(MIN_WINDOW_MS, Math.round(state.latestWindowMs * factor)) });
		return;
	}
	if (!state.selectedStart || !state.selectedEnd) return;
	const width = Math.max(MIN_WINDOW_MS, state.selectedEnd.getTime() - state.selectedStart.getTime());
	const next = Math.max(MIN_WINDOW_MS, Math.round(width * factor));
	if (state.followLatest && state.anchoredStart) {
		commitIntent({ kind: 'since', start: new Date(state.selectedEnd.getTime() - next) });
		return;
	}
	const mid = (state.selectedStart.getTime() + state.selectedEnd.getTime()) / 2;
	let start = mid - next / 2;
	let end = mid + next / 2;
	const coverage = coverageBounds();
	if (coverage) {
		if (start < coverage.start) {
			end += coverage.start - start;
			start = coverage.start;
		}
		if (end > coverage.end) {
			start -= end - coverage.end;
			end = coverage.end;
		}
		start = Math.max(coverage.start, start);
		end = Math.min(coverage.end, end);
	}
	commitIntent({ kind: 'absolute', start: new Date(start), end: new Date(end) });
}

export async function copyRangeLink(): Promise<boolean> {
	const intent = intentFromStore();
	const encoded = encodeIntent(intent);
	const url = new URL(window.location.href);
	url.searchParams.delete('start');
	url.searchParams.delete('end');
	url.searchParams.set('from', encoded.from);
	url.searchParams.set('to', encoded.to);
	applied = rangeSignature(url.search);
	replaceState(url, page.state);
	try {
		await navigator.clipboard.writeText(url.toString());
		return true;
	} catch {
		return false;
	}
}

export function intentFromStore(): RangeIntent {
	const state = get(dataSourceStore);
	return intentFromParts({
		live: state.followLatest,
		windowMs: state.latestWindowMs,
		anchoredStart: state.anchoredStart,
		start: state.selectedStart,
		end: state.selectedEnd
	});
}

export function resetRangeHistoryForTests() {
	bootstrapped = false;
	applied = '';
}

function applyIntent(intent: RangeIntent) {
	const range = get(dataSourceStore).dataRange;
	if (intent.kind === 'all') {
		dataSourceStore.showLatestWindow(range, ALL_LIVE_MS);
		return;
	}
	if (intent.kind === 'sliding') {
		dataSourceStore.showLatestWindow(range, intent.windowMs);
		return;
	}
	if (intent.kind === 'since') {
		dataSourceStore.showAnchoredLive(intent.start);
		return;
	}
	dataSourceStore.setSelectedRange(intent.start, intent.end);
}

function publishIntent(intent: RangeIntent, history: 'push' | 'replace', reload: boolean) {
	applyIntent(intent);
	const encoded = encodeIntent(intent);
	const url = new URL(window.location.href);
	url.searchParams.delete('start');
	url.searchParams.delete('end');
	url.searchParams.set('from', encoded.from);
	url.searchParams.set('to', encoded.to);
	const next = rangeSignature(url.search);
	const unchanged = next === rangeSignature(window.location.search);
	applied = next;
	if (!unchanged) {
		if (history === 'push') pushState(url, page.state);
		else replaceState(url, page.state);
	}
	if (reload) void refreshVisibleData();
}

function coverageBounds(): { start: number; end: number } | null {
	const range = get(dataSourceStore).dataRange;
	if (!range?.earliest || !range.latest) return null;
	const start = new Date(range.earliest).getTime();
	const end = new Date(range.latest).getTime();
	if (!(end > start)) return null;
	return { start, end };
}
