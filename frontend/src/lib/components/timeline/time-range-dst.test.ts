import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import { parseRangeInput } from './time-range-url';

// US clocks fall back at 2am on Nov 1, 2026, so that day has 25 hours.
// "Yesterday at 3pm" is a calendar day back, not 24 hours back.
describe('clock ranges across a DST change', () => {
	beforeAll(() => {
		vi.stubEnv('TZ', 'America/Chicago');
	});
	afterAll(() => {
		vi.unstubAllEnvs();
	});

	const noon = new Date('2026-11-01T18:00:00Z'); // 12:00 CST

	it('rolls "since 3pm" back to 3pm the day before', () => {
		const parsed = parseRangeInput('since 3pm', noon, 'local');
		if (!parsed.ok || parsed.intent.kind !== 'since') throw new Error('expected since');
		expect(parsed.intent.start.getHours()).toBe(15);
		expect(parsed.intent.start.getDate()).toBe(31);
	});

	it('keeps the wall clock for an overnight range', () => {
		const parsed = parseRangeInput('10pm - 6am', new Date('2026-11-01T20:00:00Z'), 'local');
		if (!parsed.ok || parsed.intent.kind !== 'absolute') throw new Error('expected absolute');
		expect(parsed.intent.start.getHours()).toBe(22);
		expect(parsed.intent.start.getDate()).toBe(31);
		expect(parsed.intent.end.getHours()).toBe(6);
		expect(parsed.intent.end.getDate()).toBe(1);
	});

	it('rolls a range that has not started yet back a calendar day', () => {
		const parsed = parseRangeInput('1pm - 3pm', noon, 'local');
		if (!parsed.ok || parsed.intent.kind !== 'absolute') throw new Error('expected absolute');
		expect(parsed.intent.start.getHours()).toBe(13);
		expect(parsed.intent.end.getHours()).toBe(15);
		expect(parsed.intent.start.getDate()).toBe(31);
	});
});
