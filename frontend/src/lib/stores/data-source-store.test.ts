import { beforeEach, describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import { dataSourceStore } from '#lib/stores/data-source-store';
import type { DataRange } from '#lib/services/tailscale-service';

const HOUR = 60 * 60 * 1000;
const range: DataRange = {
	earliest: '2026-03-01T00:00:00.000Z',
	latest: '2026-03-08T00:00:00.000Z',
	count: 1000
};
const latest = new Date(range.latest).getTime();

function selectedMs() {
	const s = get(dataSourceStore);
	return { start: s.selectedStart?.getTime(), end: s.selectedEnd?.getTime(), follow: s.followLatest };
}

describe('dataSourceStore page entry', () => {
	beforeEach(() => dataSourceStore.reset());

	it('opens the default latest two hours', () => {
		dataSourceStore.showLatestWindow(range, 24 * HOUR);
		dataSourceStore.enterLatestWindow(range);
		expect(selectedMs()).toEqual({ start: latest - 2 * HOUR, end: latest, follow: true });
	});

	it('keeps a handed-off latest window once, then resets', () => {
		dataSourceStore.showLatestWindow(range, 24 * HOUR);
		dataSourceStore.handOffWindow();
		dataSourceStore.enterLatestWindow(range);
		expect(selectedMs()).toEqual({ start: latest - 24 * HOUR, end: latest, follow: true });

		dataSourceStore.enterLatestWindow(range);
		expect(selectedMs()).toEqual({ start: latest - 2 * HOUR, end: latest, follow: true });
	});

	it('keeps a handed-off fixed range', () => {
		const start = new Date('2026-03-03T06:00:00.000Z');
		const end = new Date('2026-03-03T18:00:00.000Z');
		dataSourceStore.setSelectedRange(start, end);
		dataSourceStore.handOffWindow();
		dataSourceStore.enterLatestWindow(range);
		expect(selectedMs()).toEqual({ start: start.getTime(), end: end.getTime(), follow: false });
	});

	it('leaves the selection alone when there is no stored data', () => {
		dataSourceStore.enterLatestWindow({ earliest: '', latest: '', count: 0 });
		expect(selectedMs()).toEqual({ start: undefined, end: undefined, follow: true });
	});

	it('reset drops a pending handoff', () => {
		dataSourceStore.showLatestWindow(range, 24 * HOUR);
		dataSourceStore.handOffWindow();
		dataSourceStore.reset();
		dataSourceStore.enterLatestWindow(range);
		expect(selectedMs()).toEqual({ start: latest - 2 * HOUR, end: latest, follow: true });
	});
});
