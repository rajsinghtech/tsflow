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

	it('warns inline when a typed range has no stored data, and notes a partial one', async () => {
		const H = 60 * 60 * 1000;
		const start = Date.parse('2026-10-05T00:00:00.000Z');
		const coverage = { start, end: start + 60 * H };
		const spans = [
			{ start, end: start + 24 * H },
			{ start: start + 40 * H, end: start + 60 * H }
		];
		const commits: RangeIntent[] = [];
		target = document.createElement('div');
		document.body.appendChild(target);
		component = mount(TimeRangePicker, {
			target,
			props: { now, zone: 'utc', recent: [], coverage, spans, onCommit: (intent: RangeIntent) => commits.push(intent), onZone: () => undefined }
		});
		const input = document.querySelector('#time-range-input') as HTMLInputElement;
		const type = async (text: string) => {
			input.value = text;
			input.dispatchEvent(new Event('input', { bubbles: true }));
			await tick();
			return document.querySelector('#time-range-coverage')?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
		};

		expect(await type('2026-10-04T10:00:00Z to 2026-10-04T16:00:00Z')).toMatch(
			/No stored data in this range\. This range ends before stored data starts\..*Stored data: Oct 5, 12:00 AM – Oct 7, 12:00 PM/
		);
		expect(await type('2026-10-06T04:00:00Z to 2026-10-06T10:00:00Z')).toMatch(/sits in a gap/);
		expect(await type('2026-10-04T21:00:00Z to 2026-10-05T03:00:00Z')).toMatch(/^Data for 3h of 6h\. Stored data starts Oct 5, 12:00 AM\.$/);
		expect(await type('2026-10-05T02:00:00Z to 2026-10-05T08:00:00Z')).toBe('');

		// The warning does not block applying the range.
		await type('2026-10-04T10:00:00Z to 2026-10-04T16:00:00Z');
		[...document.querySelectorAll('button')].find((button) => button.textContent?.trim() === 'Apply range')?.click();
		expect(commits.at(-1)).toMatchObject({ kind: 'absolute' });
	});
});

