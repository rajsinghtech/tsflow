import { afterEach, describe, expect, it } from 'vitest';
import { mount, tick, unmount } from 'svelte';
import TimeRangePicker from './TimeRangePicker.svelte';
import TrafficBrush from './TrafficBrush.svelte';
import type { RangeIntent } from './time-range-url';

const now = new Date('2026-10-07T15:00:00.000Z');

describe('TimeRangePicker', () => {
	let target: HTMLDivElement;
	let component: ReturnType<typeof mount> | null = null;

	afterEach(async () => {
		if (component) await unmount(component);
		component = null;
		target?.remove();
	});

	it('validates typed text and commits a preset as a live range', async () => {
		const commits: RangeIntent[] = [];
		target = document.createElement('div');
		document.body.appendChild(target);
		component = mount(TimeRangePicker, {
			target,
			props: {
				now,
				zone: 'utc',
				recent: [],
				onCommit: (intent: RangeIntent) => commits.push(intent),
				onZone: () => undefined
			}
		});

		const input = document.querySelector('#time-range-input') as HTMLInputElement;
		expect(input).toBeTruthy();
		input.value = 'banana';
		input.dispatchEvent(new Event('input', { bubbles: true }));
		const apply = [...document.querySelectorAll('button')].find((button) => button.textContent?.trim() === 'Apply range');
		expect(apply, [...document.querySelectorAll('button')].map((button) => button.textContent).join('|')).toBeTruthy();
		apply?.click();
		await tick();
		expect(document.querySelector('[role="alert"]')?.textContent).toMatch(/Couldn't read/);

		const preset = [...document.querySelectorAll('button')].find((button) => button.textContent?.trim() === '1h');
		preset?.click();
		expect(commits.at(-1)).toEqual({ kind: 'sliding', windowMs: 60 * 60 * 1000 });
		expect(document.body.textContent).not.toMatch(/Back to live/);
	});
});

describe('TrafficBrush', () => {
	let target: HTMLDivElement;
	let component: ReturnType<typeof mount> | null = null;
	const rect = HTMLElement.prototype.getBoundingClientRect;

	afterEach(async () => {
		HTMLElement.prototype.getBoundingClientRect = rect;
		if (component) await unmount(component);
		component = null;
		target?.remove();
	});

	it('hatches gaps and commits the brush once when the pointer is released', async () => {
		const commits: number[][] = [];
		HTMLElement.prototype.getBoundingClientRect = () =>
			({
				x: 0,
				y: 0,
				left: 0,
				top: 0,
				right: 300,
				bottom: 64,
				width: 300,
				height: 64,
				toJSON() {
					return {};
				}
			}) as DOMRect;
		target = document.createElement('div');
		document.body.appendChild(target);
		component = mount(TrafficBrush, {
			target,
			props: {
				bins: [{ bytes: 10 }, { bytes: null }, { bytes: 0 }],
				domainStart: 0,
				domainEnd: 300_000,
				selectionStart: 0,
				selectionEnd: 100_000,
				onChange: (start: number, end: number) => commits.push([start, end])
			}
		});

		expect(document.querySelectorAll('[data-gap="true"]')).toHaveLength(1);
		const chart = document.querySelector('[aria-label="Traffic overview"]') as HTMLElement;
		chart.dispatchEvent(new PointerEvent('pointerdown', { clientX: 20, button: 0, pointerId: 1, bubbles: true }));
		chart.dispatchEvent(new PointerEvent('pointermove', { clientX: 220, pointerId: 1, bubbles: true }));
		chart.dispatchEvent(new PointerEvent('pointerup', { clientX: 220, pointerId: 1, bubbles: true }));
		chart.dispatchEvent(new PointerEvent('pointermove', { clientX: 260, pointerId: 1, bubbles: true }));
		expect(commits).toHaveLength(1);
		expect(commits[0][1]).toBeGreaterThan(commits[0][0]);
	});
});
