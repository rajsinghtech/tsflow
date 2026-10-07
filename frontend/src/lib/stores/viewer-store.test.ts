import { afterEach, describe, expect, it, vi } from 'vitest';
import { viewerReady, whenViewerReady } from './viewer-store';

describe('whenViewerReady', () => {
	afterEach(() => {
		viewerReady.set(false);
		vi.useRealTimers();
	});

	it('resolves when whoami finishes', async () => {
		viewerReady.set(false);
		let done = false;
		const wait = whenViewerReady(10_000).then(() => {
			done = true;
		});
		await Promise.resolve();
		expect(done).toBe(false);
		viewerReady.set(true);
		await wait;
		expect(done).toBe(true);
	});

	it('resolves at once when whoami already finished', async () => {
		viewerReady.set(true);
		await expect(whenViewerReady(10_000)).resolves.toBeUndefined();
	});

	it('gives up after the timeout so a slow lookup does not block the page', async () => {
		vi.useFakeTimers();
		viewerReady.set(false);
		let done = false;
		const wait = whenViewerReady(500).then(() => {
			done = true;
		});
		await vi.advanceTimersByTimeAsync(499);
		expect(done).toBe(false);
		await vi.advanceTimersByTimeAsync(1);
		await wait;
		expect(done).toBe(true);
	});
});
