import { writable } from 'svelte/store';

// Below the lg breakpoint the Traffic page puts the selection details, the
// bandwidth chart and the logs in a bottom sheet over the graph instead of
// stacking them under it, so the graph keeps the screen. Desktop is unchanged.
export const COMPACT_QUERY = '(max-width: 1023.98px)';

export const SHEET_STORAGE_KEY = 'tsflow-details-sheet';

// The sheet never gets shorter than this while it is open. Dragging the
// handle below CLOSE_BELOW and letting go closes it.
export const SHEET_MIN_HEIGHT = 160;
export const SHEET_CLOSE_BELOW = 120;

export function isCompactViewport(): boolean {
	return typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia(COMPACT_QUERY).matches;
}

type SheetStorage = Pick<Storage, 'getItem' | 'setItem'>;

function defaultStorage(): SheetStorage | null {
	try {
		return typeof localStorage === 'undefined' ? null : localStorage;
	} catch {
		return null;
	}
}

// Closed until someone opens it, so a phone starts on the graph.
export function loadSheetOpen(storage: SheetStorage | null = defaultStorage()): boolean {
	try {
		return storage?.getItem(SHEET_STORAGE_KEY) === 'open';
	} catch {
		return false;
	}
}

export function saveSheetOpen(open: boolean, storage: SheetStorage | null = defaultStorage()): void {
	try {
		storage?.setItem(SHEET_STORAGE_KEY, open ? 'open' : 'closed');
	} catch {
		// Private mode or a full quota: the sheet just won't be remembered.
	}
}

// Where a drag on the sheet handle ends up. dy is how far the finger moved,
// positive downward.
export function sheetDragResult(
	startHeight: number,
	dy: number,
	maxHeight: number
): { open: boolean; height: number } {
	const wanted = startHeight - dy;
	if (wanted < SHEET_CLOSE_BELOW) return { open: false, height: startHeight };
	const top = Math.max(SHEET_MIN_HEIGHT, maxHeight);
	return { open: true, height: Math.round(Math.min(top, Math.max(SHEET_MIN_HEIGHT, wanted))) };
}

// Height while the finger is still down, so the sheet follows it.
export function sheetDragHeight(startHeight: number, dy: number, maxHeight: number): number {
	return Math.round(Math.min(Math.max(SHEET_MIN_HEIGHT, maxHeight), Math.max(0, startHeight - dy)));
}

function createDetailsSheet() {
	const { subscribe, set } = writable(loadSheetOpen());
	const put = (open: boolean) => {
		set(open);
		saveSheetOpen(open);
	};
	let current = false;
	subscribe((value) => (current = value));
	return {
		subscribe,
		open: () => put(true),
		// Opens it for a tap on a node or a connection without changing the
		// remembered choice.
		reveal: () => set(true),
		close: () => put(false),
		toggle: () => put(!current)
	};
}

export const detailsSheet = createDetailsSheet();
