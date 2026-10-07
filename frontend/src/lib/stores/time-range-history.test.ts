import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const nav = vi.hoisted(() => ({ calls: [] as string[] }));

function setLocation(href: string) {
	const url = new URL(href);
	(globalThis as unknown as { window: { location: { href: string; search: string } } }).window.location = {
		href: url.href,
		search: url.search
	};
}

vi.mock('$app/navigation', () => ({
	pushState: (url: URL) => {
		nav.calls.push(`push ${url.search}`);
		setLocation(url.href);
	},
	replaceState: (url: URL) => {
		nav.calls.push(`replace ${url.search}`);
		setLocation(url.href);
	}
}));
// SvelteKit keeps page.url at the last real navigation for shallow entries.
vi.mock('$app/state', () => ({ page: { url: new URL('http://tsflow.test/'), state: {} } }));
vi.mock('./live-mode', () => ({ refreshVisibleData: vi.fn(async () => {}) }));

import { dataSourceStore } from './data-source-store';
import { commitIntent, intentFromStore, resetRangeHistoryForTests, syncRangeFromLocation } from './time-range-history';

const HOUR = 60 * 60 * 1000;

describe('range history', () => {
	beforeEach(() => {
		vi.stubGlobal('window', { location: { href: 'http://tsflow.test/', search: '' } });
		nav.calls = [];
		dataSourceStore.reset();
		resetRangeHistoryForTests();
	});
	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it('restores the earlier window on Back instead of writing the newer one back', () => {
		commitIntent({ kind: 'sliding', windowMs: HOUR });
		const oneHour = window.location.href;
		commitIntent({ kind: 'sliding', windowMs: 24 * HOUR });
		expect(nav.calls).toEqual(['push ?from=now-1h&to=now', 'push ?from=now-1d&to=now']);

		// Back: the address bar moves, page.url does not.
		setLocation(oneHour);
		syncRangeFromLocation();

		expect(nav.calls).toHaveLength(2);
		expect(window.location.search).toBe('?from=now-1h&to=now');
		expect(intentFromStore()).toMatchObject({ kind: 'sliding', windowMs: HOUR });
	});
});
