import { afterEach, describe, expect, it } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import TrafficBrush from './TrafficBrush.svelte';
import { rateBins } from './throughput';

const hour = 60 * 60 * 1000;
const start = Date.parse('2026-10-05T00:00:00Z');
const end = start + 4 * hour;
// 1, 2, 4 and 0.5 Gbps hours.
const points = [1, 2, 4, 0.5].map((gbps, index) => ({ time: start + index * hour, bytes: (gbps * 1e9 * 3600) / 8, durationMs: hour }));

let app: ReturnType<typeof mount> | null = null;
afterEach(() => {
	if (app) unmount(app);
	app = null;
	document.body.innerHTML = '';
});

function render(rate: ReturnType<typeof rateBins> | null) {
	app = mount(TrafficBrush, {
		target: document.body,
		props: {
			bins: [{ bytes: 10 }, { bytes: 20 }, { bytes: null }, { bytes: 5 }],
			domainStart: start,
			domainEnd: end,
			selectionStart: start + hour,
			selectionEnd: end,
			onChange: () => {},
			label: 'Traffic timeline',
			rate,
			formatTime: () => 'T'
		}
	});
	flushSync();
	return document.querySelector('[aria-label="Traffic timeline"]') as HTMLElement;
}

function hover(chart: HTMLElement, x: number) {
	chart.getBoundingClientRect = () => ({ left: 0, width: 400, top: 0, height: 80, right: 400, bottom: 80, x: 0, y: 0, toJSON: () => ({}) });
	chart.dispatchEvent(new PointerEvent('pointermove', { clientX: x, pointerType: 'mouse', bubbles: true }));
	flushSync();
}

describe('time bar chart', () => {
	it('draws a rate area with a round axis in bits per second', () => {
		const chart = render(rateBins(points, start, end, 8));
		expect(chart.querySelector('[data-rate-area]')).not.toBeNull();
		expect(chart.querySelectorAll('rect')).toHaveLength(0);
		const axis = [...chart.querySelectorAll('[data-axis-label]')].map((node) => node.textContent);
		expect(axis).toEqual(['4 Gbps', '2 Gbps']);
		const marker = chart.querySelector<HTMLElement>('[data-peak-marker]');
		expect(marker?.title).toBe('Peak 4 Gbps');
		// The 4 Gbps hour is the third of four, bins 4 and 5 of 8.
		expect(marker?.style.left).toBe('56.25%');
	});

	it('shows the rate under the pointer', () => {
		const chart = render(rateBins(points, start, end, 8));
		hover(chart, 250);
		expect(chart.querySelector('[data-chart-tooltip]')?.textContent).toContain('4 Gbps');
		chart.dispatchEvent(new PointerEvent('pointerleave', { bubbles: true }));
		flushSync();
		expect(chart.querySelector('[data-chart-tooltip]')).toBeNull();
	});

	it('ignores touch so a tap still just selects', () => {
		const chart = render(rateBins(points, start, end, 8));
		chart.getBoundingClientRect = () => ({ left: 0, width: 400, top: 0, height: 80, right: 400, bottom: 80, x: 0, y: 0, toJSON: () => ({}) });
		chart.dispatchEvent(new PointerEvent('pointermove', { clientX: 250, pointerType: 'touch', bubbles: true }));
		flushSync();
		expect(chart.querySelector('[data-chart-tooltip]')).toBeNull();
	});

	it('keeps the byte bars without a rate series', () => {
		const chart = render(null);
		expect(chart.querySelector('[data-rate-area]')).toBeNull();
		expect(chart.querySelector('[data-peak-marker]')).toBeNull();
		expect(chart.querySelectorAll('rect')).toHaveLength(3);
		expect(chart.querySelectorAll('[data-gap]')).toHaveLength(1);
		hover(chart, 150);
		expect(chart.querySelector('[data-chart-tooltip]')?.textContent).toContain('20 B');
	});
});
