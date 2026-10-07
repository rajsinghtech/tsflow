import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount, tick, unmount } from 'svelte';
import { writable } from 'svelte/store';

const stores = vi.hoisted(() => ({
	commits: [] as unknown[],
	source: null as unknown,
	coverage: null as unknown,
	window: null as unknown
}));

vi.mock('#lib/stores/data-source-store', () => {
	stores.source = writable({
		followLatest: false,
		selectedStart: new Date('2026-10-04T14:00:00Z'),
		selectedEnd: new Date('2026-10-04T20:00:00Z')
	});
	return { dataSourceStore: stores.source };
});
vi.mock('#lib/stores/traffic-shape', () => {
	stores.coverage = writable({
		start: Date.parse('2026-10-05T00:00:00Z'),
		end: Date.parse('2026-10-07T12:00:00Z')
	});
	stores.window = writable({ kind: 'none', reason: 'before-start' });
	return { storedCoverage: stores.coverage, windowCoverage: stores.window };
});
vi.mock('#lib/stores/time-range-history', () => ({
	commitIntent: (intent: unknown) => stores.commits.push(intent)
}));
vi.mock('./time-range-url', async (original) => ({
	...(await original<typeof import('./time-range-url')>()),
	loadTimeZone: () => 'utc'
}));

import EmptyRange from './EmptyRange.svelte';

describe('EmptyRange', () => {
	let target: HTMLDivElement;
	let component: ReturnType<typeof mount> | null = null;

	afterEach(async () => {
		if (component) await unmount(component);
		component = null;
		target?.remove();
	});

	it('explains the empty range, names the stored range and jumps to the latest data', async () => {
		target = document.createElement('div');
		document.body.appendChild(target);
		component = mount(EmptyRange, { target, props: {} });
		await tick();

		const status = document.querySelector('[data-testid="empty-range"]');
		expect(status?.textContent).toContain('No data in this range');
		expect(status?.textContent).toContain('This range ends before stored data starts.');
		expect(status?.textContent).toContain('Stored data: Oct 5, 12:00 AM – Oct 7, 12:00 PM');

		const jump = [...document.querySelectorAll('button')].find((button) => button.textContent?.trim() === 'Jump to latest data');
		expect(jump).toBeTruthy();
		jump?.click();
		expect(stores.commits).toEqual([{ kind: 'sliding', windowMs: 6 * 60 * 60 * 1000 }]);
	});
});
