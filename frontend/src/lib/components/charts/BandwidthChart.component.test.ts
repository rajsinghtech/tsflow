import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount, tick, unmount } from 'svelte';
import { writable } from 'svelte/store';

const getBandwidth = vi.hoisted(() => vi.fn(async () => ({ buckets: [], metadata: { bucketSeconds: 60 } })));

vi.mock('#lib/services', () => ({ tailscaleService: { getBandwidth } }));
vi.mock('#lib/utils', async () => ({ ...(await import('#lib/utils/format')) }));
vi.mock('#lib/stores/time-range-history', () => ({ commitIntent: vi.fn() }));
vi.mock('#lib/stores/tailnet-store', () => ({ selectedTailnetId: writable('one') }));
vi.mock('#lib/stores/data-source-store', () => ({
	queryTimeWindow: writable({ start: new Date('2026-10-25T22:00:00Z'), end: new Date('2026-10-26T00:00:00Z') })
}));
vi.mock('#lib/stores', () => ({
	uiStore: writable({ selectedNodeId: 'alpha', selectedEdgeId: null }),
	filteredNodes: writable([{ id: 'alpha', displayName: 'alpha', device: { id: 'nAlpha' } }]),
	filterStore: writable({ trafficTypes: ['virtual', 'subnet'] })
}));

globalThis.ResizeObserver ??= class {
	observe() {}
	unobserve() {}
	disconnect() {}
} as unknown as typeof ResizeObserver;

import BandwidthChart from './BandwidthChart.svelte';

describe('BandwidthChart node view', () => {
	let target: HTMLDivElement;
	let component: ReturnType<typeof mount> | null = null;

	afterEach(async () => {
		if (component) await unmount(component);
		component = null;
		target?.remove();
	});

	it('asks for the selected node with the traffic type filter', async () => {
		target = document.createElement('div');
		document.body.appendChild(target);
		component = mount(BandwidthChart, { target, props: {} });
		await tick();
		await tick();

		expect(getBandwidth).toHaveBeenCalled();
		const [, , nodeId, , types] = getBandwidth.mock.calls.at(-1) as unknown as unknown[];
		expect(nodeId).toBe('nAlpha');
		expect(types).toEqual(['virtual', 'subnet']);
		expect(target.textContent).not.toContain('node all types');
	});
});
