import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount, tick, unmount } from 'svelte';
import { writable } from 'svelte/store';

vi.mock('#lib/stores/data-source-store', () => ({
	dataSourceStore: writable({
		followLatest: false,
		dataRange: { earliest: '2026-10-04T12:00:00Z', latest: '2026-10-04T20:00:00Z' },
		selectedStart: new Date('2026-10-04T14:00:00Z'),
		selectedEnd: new Date('2026-10-04T16:00:00Z')
	})
}));
vi.mock('#lib/stores/traffic-shape', () => ({
	trafficPoints: writable([
		{ time: Date.parse('2026-10-04T14:00:00Z'), bytes: 1000 },
		{ time: Date.parse('2026-10-04T15:00:00Z'), bytes: 2000 }
	]),
	trafficShapeError: writable('')
}));
vi.mock('#lib/stores/traffic-detail', () => ({
	trafficDetail: writable(null),
	loadTrafficDetail: () => {}
}));
vi.mock('#lib/stores/time-range-history', () => ({ commitIntent: () => {} }));

import TimelineStrip from './TimelineStrip.svelte';

describe('TimelineStrip', () => {
	let target: HTMLDivElement;
	let component: ReturnType<typeof mount> | null = null;

	afterEach(async () => {
		if (component) await unmount(component);
		component = null;
		target?.remove();
	});

	it('says the chart is not filtered by traffic type', async () => {
		target = document.createElement('div');
		document.body.appendChild(target);
		component = mount(TimelineStrip, { target, props: { zone: 'utc' } });
		await tick();
		const scope = document.querySelector('[data-testid="timebar-scope"]');
		expect(scope?.textContent).toContain('All traffic');
		expect(scope?.getAttribute('title')).toContain("Traffic Type filter doesn't change this chart");
	});
});
