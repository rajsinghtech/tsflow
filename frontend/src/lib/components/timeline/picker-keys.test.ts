import { describe, expect, it } from 'vitest';
import { closesPicker } from './picker-keys';

const key = (k: string, mods: Partial<Record<'metaKey' | 'ctrlKey' | 'altKey', boolean>> = {}) => ({
	key: k,
	metaKey: false,
	ctrlKey: false,
	altKey: false,
	...mods
});

describe('closesPicker', () => {
	it('closes an open picker on Escape', () => {
		expect(closesPicker(key('Escape'), true)).toBe(true);
	});

	it('ignores a closed picker, other keys and modified Escape', () => {
		expect(closesPicker(key('Escape'), false)).toBe(false);
		expect(closesPicker(key('Enter'), true)).toBe(false);
		expect(closesPicker(key('Escape', { metaKey: true }), true)).toBe(false);
	});
});
