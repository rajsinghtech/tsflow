import { describe, expect, it } from 'vitest';
import { DEFAULT_NEW_PAIR_LOOKBACK, NEW_PAIR_LOOKBACKS, newPairPath, lookbackNotice } from './new-pairs';

describe('new pair query', () => {
	it('defaults the lookback to 7 days and keeps paging', () => {
		expect(DEFAULT_NEW_PAIR_LOOKBACK).toBe('7d');
		expect(NEW_PAIR_LOOKBACKS.map((item) => item.value)).toEqual(['24h', '7d', '30d']);
		const path = newPairPath(new Date('2026-03-02T12:00:00Z'), new Date('2026-03-02T13:00:00Z'), {
			limit: 20,
			offset: 20,
			trafficTypes: ['virtual', 'subnet']
		});
		expect(path).toContain('lookback=7d');
		expect(path).toContain('offset=20');
		expect(path).toContain('trafficTypes=virtual%2Csubnet');
		expect(path.startsWith('/analytics/new-pairs?')).toBe(true);
	});

	it('sends an explicit lookback', () => {
		const path = newPairPath(new Date('2026-03-02T12:00:00Z'), new Date('2026-03-02T13:00:00Z'), {
			lookback: '24h'
		});
		expect(path).toContain('lookback=24h');
	});
});

describe('lookbackNotice', () => {
	const fmt = (d: Date) => d.toISOString().slice(0, 10);
	it('is silent when the lookback is covered or coverage is unknown', () => {
		expect(lookbackNotice({ lookbackComplete: true, dataStart: '2026-03-01T00:00:00Z' }, fmt)).toBeNull();
		expect(lookbackNotice(undefined, fmt)).toBeNull();
		expect(lookbackNotice({}, fmt)).toBeNull();
	});
	it('names the data start when the lookback reaches past it', () => {
		expect(lookbackNotice({ lookbackComplete: false, dataStart: '2026-02-27T12:00:00Z' }, fmt)).toContain('2026-02-27');
	});
	it('handles a store with no data', () => {
		expect(lookbackNotice({ lookbackComplete: false }, fmt)).toContain('No stored data');
	});
});
