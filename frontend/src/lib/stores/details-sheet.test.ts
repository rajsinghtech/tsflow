import { describe, expect, it } from 'vitest';
import {
	SHEET_CLOSE_BELOW,
	SHEET_MIN_HEIGHT,
	SHEET_STORAGE_KEY,
	loadSheetOpen,
	saveSheetOpen,
	sheetDragHeight,
	sheetDragResult
} from './details-sheet';

function memory(initial: Record<string, string> = {}) {
	const data = { ...initial };
	return {
		data,
		getItem: (key: string) => data[key] ?? null,
		setItem: (key: string, value: string) => {
			data[key] = value;
		}
	};
}

describe('details sheet storage', () => {
	it('starts closed when nothing is stored', () => {
		expect(loadSheetOpen(memory())).toBe(false);
		expect(loadSheetOpen(null)).toBe(false);
	});

	it('remembers open and closed', () => {
		const store = memory();
		saveSheetOpen(true, store);
		expect(store.data[SHEET_STORAGE_KEY]).toBe('open');
		expect(loadSheetOpen(store)).toBe(true);
		saveSheetOpen(false, store);
		expect(loadSheetOpen(store)).toBe(false);
	});

	it('survives storage that throws', () => {
		const broken = {
			getItem: () => {
				throw new Error('denied');
			},
			setItem: () => {
				throw new Error('quota');
			}
		};
		expect(loadSheetOpen(broken)).toBe(false);
		expect(() => saveSheetOpen(true, broken)).not.toThrow();
	});
});

describe('sheetDragResult', () => {
	it('closes when dragged below the close line and keeps the old height for next time', () => {
		expect(sheetDragResult(400, 400 - SHEET_CLOSE_BELOW + 1, 700)).toEqual({ open: false, height: 400 });
	});

	it('resizes within the allowed range', () => {
		expect(sheetDragResult(400, 100, 700)).toEqual({ open: true, height: 300 });
		expect(sheetDragResult(400, -500, 700)).toEqual({ open: true, height: 700 });
		expect(sheetDragResult(400, 400 - SHEET_CLOSE_BELOW - 10, 700)).toEqual({ open: true, height: SHEET_MIN_HEIGHT });
	});

	it('never goes under the minimum even when the space is tiny', () => {
		expect(sheetDragResult(200, -50, 100)).toEqual({ open: true, height: SHEET_MIN_HEIGHT });
	});
});

describe('sheetDragHeight', () => {
	it('follows the finger down to zero and up to the cap', () => {
		expect(sheetDragHeight(400, 50, 700)).toBe(350);
		expect(sheetDragHeight(400, 500, 700)).toBe(0);
		expect(sheetDragHeight(400, -900, 700)).toBe(700);
	});
});
