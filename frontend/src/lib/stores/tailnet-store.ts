import { get, writable } from 'svelte/store';
import {
	chooseTailnet,
	resetTailnetQueryForTests,
	setActiveTailnet,
	setTailnetLoader,
	tailnetParamFromSearch,
	type TailnetInfo,
	type TailnetPollerStatus
} from '#lib/services/tailnet-query';
import { uiStore } from './ui-store';
import { dataSourceStore } from './data-source-store';
import { clearNetworkData, loadNetworkData } from './network-store';
import { clearPolicyData, fetchAndRenderPolicy } from './policy-store';
import { clearStatsData, loadStats } from './stats-store';

export const tailnets = writable<TailnetInfo[]>([]);
export const selectedTailnetId = writable<string | null>(null);

let readSearch = (): string => (typeof window === 'undefined' ? '' : window.location.search);

export function setTailnetSearchReader(reader: () => string) {
	readSearch = reader;
}

function currentPath(): string {
	if (typeof window === 'undefined') return '/';
	return window.location.pathname;
}

function normalizePoller(value: unknown): TailnetPollerStatus {
	const poller = value && typeof value === 'object' ? (value as Record<string, unknown>) : {};
	return {
		running: poller.running === true,
		lastPollTime: typeof poller.lastPollTime === 'string' ? poller.lastPollTime : '',
		pollErrors: typeof poller.pollErrors === 'number' ? poller.pollErrors : 0,
		lastError: typeof poller.lastError === 'string' ? poller.lastError : ''
	};
}

function normalizeTailnet(value: unknown): TailnetInfo | null {
	if (!value || typeof value !== 'object') return null;
	const raw = value as Record<string, unknown>;
	if (typeof raw.id !== 'string' || raw.id.trim() === '') return null;
	const displayName = typeof raw.displayName === 'string' && raw.displayName.trim() !== '' ? raw.displayName : raw.id;
	return {
		id: raw.id,
		displayName,
		poller: normalizePoller(raw.poller)
	};
}

async function fetchTailnetList(): Promise<TailnetInfo[] | null> {
	const response = await fetch('/api/tailnets');
	// An older backend has no list endpoint. Treat that as one tailnet.
	if (response.status === 404 || !response.ok) return null;
	const data = await response.json();
	const raw = Array.isArray(data?.tailnets) ? data.tailnets : [];
	return raw.map(normalizeTailnet).filter((tailnet: TailnetInfo | null): tailnet is TailnetInfo => tailnet !== null);
}

function applyList(list: TailnetInfo[]) {
	tailnets.set(list);
	const chosen = chooseTailnet(list, tailnetParamFromSearch(readSearch()));
	selectedTailnetId.set(chosen);
	setActiveTailnet(chosen);
}

export async function loadTailnets(): Promise<void> {
	try {
		const list = await fetchTailnetList();
		applyList(list ?? []);
	} catch (err) {
		console.error('Failed to load tailnets:', err);
		applyList([]);
	}
}

export async function refreshTailnetStatus(): Promise<void> {
	try {
		const list = await fetchTailnetList();
		if (!list || list.length < 2) return;
		tailnets.set(list);
	} catch (err) {
		console.error('Failed to refresh tailnets:', err);
	}
}

export function resetTailnetCaches(): void {
	dataSourceStore.reset();
	clearNetworkData();
	clearStatsData();
	clearPolicyData();
	uiStore.clearSelection();
}

async function reloadCurrentView(): Promise<void> {
	const path = currentPath();
	if (path === '/analytics') {
		await loadStats();
		return;
	}
	if (path === '/policy') {
		await fetchAndRenderPolicy();
		return;
	}
	await loadNetworkData();
}

export async function selectTailnet(id: string): Promise<void> {
	if (!get(tailnets).some((tailnet) => tailnet.id === id)) return;
	if (get(selectedTailnetId) === id) return;
	setActiveTailnet(id);
	selectedTailnetId.set(id);
	resetTailnetCaches();
	await reloadCurrentView();
}

export function resetTailnetStateForTests() {
	resetTailnetQueryForTests();
	tailnets.set([]);
	selectedTailnetId.set(null);
	readSearch = () => (typeof window === 'undefined' ? '' : window.location.search);
}

setTailnetLoader(loadTailnets);
