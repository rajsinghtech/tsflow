import { describe, expect, it } from 'vitest';
import { parseGoDuration, refreshIntervalMs } from './poll-interval';

describe('parseGoDuration', () => {
	it('reads Go duration strings', () => {
		expect(parseGoDuration('5m0s')).toBe(5 * 60 * 1000);
		expect(parseGoDuration('1h0m0s')).toBe(60 * 60 * 1000);
		expect(parseGoDuration('30s')).toBe(30_000);
		expect(parseGoDuration('not-a-duration')).toBeNull();
		expect(parseGoDuration('')).toBeNull();
	});
});

describe('refreshIntervalMs', () => {
	it('follows the poll cadence and ignores a faster fallback', () => {
		expect(refreshIntervalMs('5m0s', 60_000)).toBe(5 * 60 * 1000);
	});

	it('does not reload faster than every 30 seconds', () => {
		expect(refreshIntervalMs('1s')).toBe(30_000);
	});

	it('uses the fallback when the status has no interval', () => {
		expect(refreshIntervalMs(undefined, 60_000)).toBe(60_000);
		expect(refreshIntervalMs(null)).toBe(5 * 60 * 1000);
	});
});
