const UNIT_MS: Record<string, number> = {
	h: 3_600_000,
	m: 60_000,
	s: 1_000,
	ms: 1,
	us: 0.001,
	µs: 0.001,
	μs: 0.001,
	ns: 0.000001
};

// Go's time.Duration.String uses compound units, for example "5m0s" or "1h0m0s".
export function parseGoDuration(value: string | null | undefined): number | null {
	if (!value) return null;
	const trimmed = value.trim().replace(/\s+/g, '');
	if (!trimmed) return null;
	const token = /(\d+(?:\.\d+)?)(ns|us|µs|μs|ms|h|m|s)/g;
	let consumed = '';
	let total = 0;
	for (const match of trimmed.matchAll(token)) {
		consumed += match[0];
		total += Number(match[1]) * UNIT_MS[match[2]];
	}
	if (!consumed || consumed.length !== trimmed.length) return null;
	if (!Number.isFinite(total) || total <= 0) return null;
	return Math.round(total);
}

export const MIN_REFRESH_MS = 30_000;
export const DEFAULT_REFRESH_MS = 5 * 60 * 1000;

// Match the poll cadence, but never faster than a graph of this size can
// comfortably reload. An unknown status keeps the historical 5 minute pace.
export function refreshIntervalMs(raw: string | null | undefined, fallback = DEFAULT_REFRESH_MS): number {
	const parsed = parseGoDuration(raw);
	return Math.max(MIN_REFRESH_MS, parsed ?? fallback);
}

export function nextPollAt(lastMs: number, intervalMs: number, now: number): number {
	const interval = Math.max(1_000, intervalMs);
	const elapsed = now - lastMs;
	if (elapsed <= interval) return lastMs + interval;
	return lastMs + (Math.floor(elapsed / interval) + 1) * interval;
}

export function pollerIsBehind(lastMs: number, intervalMs: number, now: number): boolean {
	if (!Number.isFinite(lastMs) || lastMs <= 0) return false;
	return now - lastMs > Math.max(1_000, intervalMs) * 2;
}
