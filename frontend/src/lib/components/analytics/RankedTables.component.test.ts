import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount, tick, unmount } from 'svelte';
import { writable } from 'svelte/store';

vi.mock('#lib/stores', () => {
	const table = (rows: unknown[]) =>
		writable({ rows, offset: 0, limit: 25, count: rows.length, hasMore: false, loading: false, error: '' });
	return {
		filterStore: { setSearch: () => {} },
		uiStore: { selectNode: () => {} },
		loadRankedPairs: () => {},
		loadRankedTalkers: () => {},
		rankSearch: writable(''),
		rankSort: writable('bytes'),
		setRankSearch: () => {},
		setRankSort: () => {},
		rankedTalkers: table([]),
		rankedPairs: table([
			{
				srcNodeId: 'n-alpha',
				srcHostname: 'alpha',
				dstNodeId: 'n-charlie',
				dstHostname: 'charlie',
				totalBytes: 1024,
				flowCount: 3
			}
		])
	};
});

vi.mock('#lib/utils', () => ({ formatBytes: (n: number) => `${n} B` }));

import RankedTables from './RankedTables.svelte';

describe('RankedTables pairs', () => {
	let target: HTMLDivElement;
	let component: ReturnType<typeof mount> | null = null;

	afterEach(async () => {
		if (component) await unmount(component);
		component = null;
		target?.remove();
	});

	// Stored pairs are sorted by node id, so the table must not read as
	// source and destination.
	it('labels pairs as unordered endpoints', async () => {
		target = document.createElement('div');
		document.body.appendChild(target);
		component = mount(RankedTables, { target, props: {} });
		await tick();
		const text = document.body.textContent ?? '';
		expect(text).toContain('Endpoint A');
		expect(text).toContain('Endpoint B');
		expect(text).not.toContain('Destination');
		expect(text).not.toContain('→');
		expect(text).toContain('↔');
	});
});
