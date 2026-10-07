import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import { mount, unmount } from 'svelte';
import TimelineSlider from './TimelineSlider.svelte';
import { dataSourceStore } from '#lib/stores/data-source-store';
import { resetTailnetQueryForTests } from '#lib/services/tailnet-query';
import { resetTailnetStateForTests } from '#lib/stores/tailnet-store';

const stray = Date.parse('2026-09-29T09:45:00.000Z');
const coverageStart = Date.parse('2026-10-05T00:00:00.000Z');
const coverageEnd = Date.parse('2026-10-06T22:19:00.000Z');
const gapStart = Date.parse('2026-10-06T02:00:00.000Z');
const gapEnd = Date.parse('2026-10-06T05:00:00.000Z');

function json(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

function bucketsFor(startMs: number, endMs: number) {
	const buckets = [];
	let cursor = Math.floor(startMs / 3_600_000) * 3_600_000;
	const strayHour = Math.floor(stray / 3_600_000) * 3_600_000;
	while (cursor < endMs) {
		let bytes = 0;
		if (cursor === strayHour) bytes = 1_500;
		else if (cursor >= coverageStart && cursor < coverageEnd && (cursor < gapStart || cursor >= gapEnd)) {
			bytes = 8_000_000;
		}
		if (bytes > 0) {
			buckets.push({
				time: new Date(cursor).toISOString(),
				txBytes: bytes,
				rxBytes: Math.round(bytes * 0.2)
			});
		}
		cursor += 3_600_000;
	}
	return buckets;
}

function button(label: string): HTMLButtonElement {
	const match = [...document.querySelectorAll('button')].find((item) => item.textContent?.trim() === label);
	if (!match) throw new Error(`missing button ${label}`);
	return match as HTMLButtonElement;
}

describe('TimelineSlider', () => {
	let target: HTMLDivElement;
	let component: Record<string, unknown> | null = null;
	const changes: number[] = [];

	beforeEach(() => {
		changes.length = 0;
		dataSourceStore.reset();
		resetTailnetQueryForTests();
		resetTailnetStateForTests();
		document.body.innerHTML = '';
		target = document.createElement('div');
		document.body.appendChild(target);
		vi.stubGlobal('fetch', async (input: RequestInfo | URL) => {
			const url = String(input);
			if (url.includes('/api/tailnets')) return json({ tailnets: [] });
			if (url.includes('/flow-logs/range')) {
				return json({
					earliest: new Date(stray).toISOString(),
					latest: new Date(coverageEnd).toISOString(),
					count: 80
				});
			}
			if (url.includes('/poller/status')) {
				return json({
					running: true,
					lastPollTime: new Date(coverageEnd).toISOString(),
					lastPollCount: 10,
					totalPolled: 80,
					pollErrors: 0,
					pollInterval: '5m0s',
					database: { tableCounts: { flow_logs_current: 80 }, dbSizeBytes: 10, dataRange: { count: 80 } }
				});
			}
			if (url.includes('/bandwidth')) {
				const params = new URL(url, 'http://localhost').searchParams;
				const start = Date.parse(params.get('start') || '');
				const end = Date.parse(params.get('end') || '');
				const buckets = bucketsFor(start, end);
				return json({
					buckets,
					metadata: { count: buckets.length, start: params.get('start'), end: params.get('end'), bucketSeconds: 3600 }
				});
			}
			return json({});
		});
	});

	afterEach(async () => {
		if (component) await unmount(component);
		component = null;
		target.remove();
		vi.unstubAllGlobals();
		dataSourceStore.reset();
	});

	async function render() {
		component = mount(TimelineSlider, {
			target,
			props: {
				onWindowChange: () => {
					changes.push(1);
				}
			}
		}) as Record<string, unknown>;
		await vi.waitFor(() => {
			expect(button('15m')).toBeTruthy();
			expect(document.querySelectorAll('svg rect').length).toBeGreaterThan(3);
		});
	}

	it('keeps a preset in live mode and shows traffic instead of a bare slider', async () => {
		await render();
		expect(document.body.textContent).not.toMatch(/Selected/);
		expect(document.body.textContent).not.toMatch(/Available/);
		expect(document.querySelector('.live-pulse')).toBeTruthy();
		expect(document.body.textContent).toContain('coverage');

		button('1h').click();
		await vi.waitFor(() => {
			expect(get(dataSourceStore).followLatest).toBe(true);
			expect(get(dataSourceStore).latestWindowMs).toBe(60 * 60 * 1000);
			expect(button('1h').getAttribute('aria-pressed')).toBe('true');
		});
	});

	it('leaves live mode when the window is brushed and offers one way back', async () => {
		await render();
		const end = get(dataSourceStore).selectedEnd;
		const start = get(dataSourceStore).selectedStart;
		expect(end && start).toBeTruthy();
		dataSourceStore.setSelectedRange(new Date(end!.getTime() - 3 * 60 * 60 * 1000), end!);
		await vi.waitFor(() => {
			expect(document.body.textContent).toContain('Back to live');
			expect(document.body.textContent).toContain('Pinned');
		});
		expect(document.querySelector('.live-pulse')).toBeNull();
		expect(get(dataSourceStore).followLatest).toBe(false);

		button('Back to live').click();
		await vi.waitFor(() => {
			expect(get(dataSourceStore).followLatest).toBe(true);
			expect(document.querySelector('.live-pulse')).toBeTruthy();
		});
	});

	it('adjusts the focused edge from the keyboard and rejects an impossible time', async () => {
		await render();
		const handle = document.querySelector('[aria-label="Window start"]') as HTMLButtonElement;
		expect(handle).toBeTruthy();
		const before = get(dataSourceStore).selectedStart!.getTime();
		handle.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }));
		await vi.waitFor(() => {
			expect(get(dataSourceStore).followLatest).toBe(false);
			expect(get(dataSourceStore).selectedStart!.getTime()).toBeLessThan(before);
		});

		const input = document.querySelector('[aria-label="Start"]') as HTMLInputElement;
		input.value = '2020-01-01T00:00';
		input.dispatchEvent(new Event('input', { bubbles: true }));
		input.dispatchEvent(new Event('change', { bubbles: true }));
		await vi.waitFor(() => {
			expect(document.body.textContent).toMatch(/outside the data we have/);
		});
		expect(get(dataSourceStore).selectedStart!.getTime()).not.toBe(Date.parse('2020-01-01T00:00'));
	});
});
