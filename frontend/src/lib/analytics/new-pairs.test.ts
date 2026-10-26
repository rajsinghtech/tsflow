import { describe, expect, it } from 'vitest';
import { lookbackNotice } from './new-pairs';

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
		// The Me summary sends dataStart: null when nothing is stored.
		expect(lookbackNotice({ lookbackComplete: false, dataStart: null }, fmt)).toContain('No stored data');
	});
});
