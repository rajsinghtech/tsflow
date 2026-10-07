import { describe, expect, it } from 'vitest';
import { DEFAULT_NEW_PAIR_LOOKBACK, NEW_PAIR_LOOKBACKS, newPairPath } from './new-pairs';

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
