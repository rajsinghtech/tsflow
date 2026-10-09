import { get } from 'svelte/store';
import { dataSourceStore } from './data-source-store';
import { refreshIntervalMs } from '#lib/utils/poll-interval';

// Runs a reload only while the window is live. Pinning the window stops the
// timer and leaves it armed, so returning to live resumes the same cadence.
export function createLiveRefresh(run: () => void, onRunning?: (running: boolean) => void) {
	let armed = false;
	let fallbackMs = refreshIntervalMs(undefined);
	let timer: ReturnType<typeof setInterval> | null = null;
	let activeMs = 0;
	let running = false;

	function publish(next: boolean) {
		if (next === running) return;
		running = next;
		onRunning?.(next);
	}

	function clearTimer() {
		if (!timer) return;
		clearInterval(timer);
		timer = null;
		activeMs = 0;
	}

	function sync() {
		const state = get(dataSourceStore);
		const nextMs = refreshIntervalMs(state.pollerStatus?.pollInterval, fallbackMs);
		if (!armed || !state.followLatest) {
			clearTimer();
			publish(false);
			return;
		}
		if (timer && activeMs === nextMs) return;
		clearTimer();
		activeMs = nextMs;
		timer = setInterval(run, nextMs);
		publish(true);
	}

	const unsubscribe = dataSourceStore.subscribe(() => {
		sync();
	});

	return {
		start(fallback?: number) {
			if (fallback && fallback > 0) fallbackMs = fallback;
			armed = true;
			sync();
		},
		stop() {
			armed = false;
			sync();
		},
		isArmed() {
			return armed;
		},
		dispose() {
			armed = false;
			clearTimer();
			publish(false);
			unsubscribe();
		}
	};
}
