import { describe, expect, it } from 'vitest';
import { bucketSpanMs, expiryState, jsonList, keyFlags } from './insights';

describe('bucketSpanMs', () => {
	it('reads widened bars from the gap', () => {
		const b = (start: string) => ({ start, total: 0, failed: 0, categories: {} });
		expect(bucketSpanMs({ bucket: 'day', buckets: [b('2026-03-01T00:00:00Z'), b('2026-03-03T00:00:00Z')] })).toBe(2 * 86_400_000);
		expect(bucketSpanMs({ bucket: 'hour', buckets: [b('2026-03-01T00:00:00Z')] })).toBe(3_600_000);
	});
});

describe('key and device helpers', () => {
	it('lists key flags', () => {
		expect(keyFlags({ reusable: true, ephemeral: false, preauthorized: true })).toEqual(['reusable', 'pre-approved']);
	});
	it('flattens tag and route values', () => {
		expect(jsonList(['tag:a', 'tag:b'])).toEqual(['tag:a', 'tag:b']);
		expect(jsonList({ routes: ['192.0.2.0/24'] })).toEqual(['192.0.2.0/24']);
		expect(jsonList(null)).toEqual([]);
	});
	it('works out key expiry state', () => {
		const now = new Date('2026-03-01T00:00:00Z');
		expect(expiryState({ keyExpiryDisabled: true }, now)).toBe('disabled');
		expect(expiryState({ keyExpiry: '2026-02-01T00:00:00Z' }, now)).toBe('expired');
		expect(expiryState({ keyExpiry: '2026-03-05T00:00:00Z' }, now)).toBe('soon');
		expect(expiryState({ keyExpiry: '2026-09-05T00:00:00Z' }, now)).toBe('ok');
		expect(expiryState({ keyExpired: { at: '2026-02-01T00:00:00Z', eventId: 1 } }, now)).toBe('expired');
		expect(expiryState({}, now)).toBe('unknown');
	});
});
