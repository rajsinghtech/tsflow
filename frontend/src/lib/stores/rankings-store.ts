import { get, writable } from 'svelte/store';
import type { RankedPair, RankedTalker, RankMetadata, RankQueryParams, RankSort } from '#lib/analytics/rank-query';
import { RANK_PAGE_SIZE } from '#lib/analytics/rank-query';
import { tailscaleService } from '#lib/services/tailscale-service';
import { dataSourceStore, queryTimeWindow } from './data-source-store';

export interface RankTableState<T> {
	rows: T[];
	offset: number;
	limit: number;
	count: number;
	hasMore: boolean;
	loading: boolean;
	error: string | null;
}

interface LoadSlot<T> {
	state: ReturnType<typeof writable<RankTableState<T>>>;
	gen: number;
	abort: AbortController | null;
	fallbackError: string;
	fetchRows: (
		query: RankQueryParams,
		signal: AbortSignal
	) => Promise<{ rows: T[]; metadata?: RankMetadata }>;
}

function emptyTable<T>(): RankTableState<T> {
	return {
		rows: [],
		offset: 0,
		limit: RANK_PAGE_SIZE,
		count: 0,
		hasMore: false,
		loading: false,
		error: null
	};
}

function readMeta(meta: RankMetadata | undefined, offset: number, rowCount: number) {
	return {
		offset: meta?.offset ?? offset,
		count: meta?.count ?? rowCount,
		hasMore: meta?.hasMore === true
	};
}

const talkerState = writable<RankTableState<RankedTalker>>(emptyTable());
const pairState = writable<RankTableState<RankedPair>>(emptyTable());

const talkersSlot: LoadSlot<RankedTalker> = {
	state: talkerState,
	gen: 0,
	abort: null,
	fallbackError: 'Failed to load talkers',
	fetchRows: async (query, signal) => {
		const response = await tailscaleService.getRankedTalkers(query, signal);
		return { rows: response.talkers ?? [], metadata: response.metadata };
	}
};

const pairsSlot: LoadSlot<RankedPair> = {
	state: pairState,
	gen: 0,
	abort: null,
	fallbackError: 'Failed to load pairs',
	fetchRows: async (query, signal) => {
		const response = await tailscaleService.getRankedPairs(query, signal);
		return { rows: response.pairs ?? [], metadata: response.metadata };
	}
};

export const rankSort = writable<RankSort>('bytes');
export const rankedTalkers = talkerState;
export const rankedPairs = pairState;

let refreshTimer: ReturnType<typeof setInterval> | null = null;

async function loadSlot<T>(slot: LoadSlot<T>, offset: number): Promise<void> {
	const gen = ++slot.gen;
	slot.abort?.abort();
	const controller = new AbortController();
	slot.abort = controller;
	const signal = controller.signal;
	const previousOffset = get(slot.state).offset;
	slot.state.update((current) => ({ ...current, offset, loading: true, error: null }));

	try {
		if (get(dataSourceStore).followLatest) {
			await dataSourceStore.fetchDataRange();
		}
		if (gen !== slot.gen || signal.aborted) return;
		const { start, end } = get(queryTimeWindow);
		const response = await slot.fetchRows(
			{ start, end, limit: RANK_PAGE_SIZE, offset, sort: get(rankSort) },
			signal
		);
		if (gen !== slot.gen || signal.aborted) return;
		const meta = readMeta(response.metadata, offset, response.rows.length);
		slot.state.set({
			rows: response.rows,
			offset: meta.offset,
			limit: RANK_PAGE_SIZE,
			count: meta.count,
			hasMore: meta.hasMore,
			loading: false,
			error: null
		});
	} catch (err) {
		if (gen !== slot.gen || signal.aborted) return;
		slot.state.update((current) => ({
			...current,
			offset: previousOffset,
			loading: false,
			error: err instanceof Error ? err.message : slot.fallbackError
		}));
	}
}

function invalidate(slot: LoadSlot<unknown>) {
	slot.gen += 1;
	slot.abort?.abort();
	slot.abort = null;
}

export function loadRankedTalkers(offset = 0): Promise<void> {
	return loadSlot(talkersSlot, offset);
}

export function loadRankedPairs(offset = 0): Promise<void> {
	return loadSlot(pairsSlot, offset);
}

// resetOffset drops both tables back to the first page. Window and sort
// changes use that. A refresh keeps the current page.
export async function loadRankings(resetOffset = false): Promise<void> {
	if (resetOffset) {
		rankedTalkers.update((current) => ({ ...current, offset: 0 }));
		rankedPairs.update((current) => ({ ...current, offset: 0 }));
	}
	const talkerOffset = get(rankedTalkers).offset;
	const pairOffset = get(rankedPairs).offset;
	await Promise.all([loadRankedTalkers(talkerOffset), loadRankedPairs(pairOffset)]);
}

export async function setRankSort(next: RankSort): Promise<void> {
	if (get(rankSort) === next) return;
	rankSort.set(next);
	await loadRankings(true);
}

export function clearRankingsData(): void {
	invalidate(talkersSlot);
	invalidate(pairsSlot);
	rankedTalkers.set(emptyTable());
	rankedPairs.set(emptyTable());
}

export function startRankingsRefresh(intervalMs = 60_000): void {
	stopRankingsRefresh();
	void loadRankings(false);
	refreshTimer = setInterval(() => {
		void loadRankings(false);
	}, intervalMs);
}

export function stopRankingsRefresh(): void {
	if (refreshTimer) {
		clearInterval(refreshTimer);
		refreshTimer = null;
	}
	invalidate(talkersSlot);
	invalidate(pairsSlot);
}

export function resetRankingsForTests(): void {
	stopRankingsRefresh();
	rankSort.set('bytes');
	rankedTalkers.set(emptyTable());
	rankedPairs.set(emptyTable());
}
