import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount, tick, unmount } from 'svelte';
import { writable } from 'svelte/store';
import type { NetworkLog } from '#lib/types';

const logs = vi.hoisted(() => ({ raw: null as unknown }));

vi.mock('#lib/utils', async () => ({
	...(await import('#lib/utils/format')),
	...(await import('#lib/utils/ip-utils')),
	...(await import('#lib/utils/protocol'))
}));

vi.mock('#lib/stores', () => {
	logs.raw = writable<NetworkLog[]>([]);
	const filters = writable({ search: '', trafficTypes: [] as string[] });
	return {
		rawLogs: logs.raw,
		uiStore: Object.assign(writable({ selectedNodeId: null, selectedEdgeId: null }), { selectEdge: vi.fn() }),
		filteredNodes: writable([]),
		filteredEdges: writable([]),
		processedNetwork: writable({ nodes: [], links: [] }),
		filterStore: filters,
		debouncedFilterStore: filters,
		devices: writable([]),
		services: writable({}),
		primaryMatchedNodes: writable([]),
		hasStoredData: writable(true)
	};
});

import LogViewer from './LogViewer.svelte';

const windowLog: NetworkLog = {
	logged: '2026-10-25T19:00:00Z',
	nodeId: 'n1',
	start: '2026-10-25T16:00:00Z',
	end: '2026-10-25T19:00:00Z',
	virtualTraffic: [{ proto: 6, src: '100.64.0.1:443', dst: '100.64.0.2:5000', txBytes: 10, rxBytes: 5, txPkts: 1, rxPkts: 1 }],
	subnetTraffic: [],
	physicalTraffic: []
};

describe('LogViewer time column', () => {
	let target: HTMLDivElement;
	let component: ReturnType<typeof mount> | null = null;

	afterEach(async () => {
		if (component) await unmount(component);
		component = null;
		target?.remove();
	});

	it('says the time is the window end, not when the traffic happened', async () => {
		(logs.raw as import('svelte/store').Writable<NetworkLog[]>).set([windowLog]);
		target = document.createElement('div');
		document.body.appendChild(target);
		component = mount(LogViewer, { target, props: {} });
		await tick();

		const headers = [...target.querySelectorAll('th')].map((th) => th.textContent?.trim() ?? '');
		expect(headers[0]).toContain('Window end');
		expect(headers).not.toContain('Time');
		expect(target.querySelector('th')?.getAttribute('title')).toMatch(/window ends/);
		expect(target.textContent).toContain('Showing totals for the window');
		expect(target.textContent).not.toContain('Showing recent logs');
		expect(target.textContent).toMatch(/through \d/);
	});
});
