import { describe, expect, it } from 'vitest';
import { pollFailure, realTime } from './poll-health';

describe('pollFailure', () => {
	it('is quiet when the last poll worked', () => {
		expect(pollFailure(null)).toBeNull();
		expect(pollFailure({})).toBeNull();
		expect(pollFailure({ lastError: '  ' })).toBeNull();
	});

	it('points at credentials for a 403', () => {
		const failure = pollFailure({
			lastError: 'poll chunk 1/48 failed: failed to fetch network logs: HTTP 403: {"message": "API token invalid"}'
		});
		expect(failure?.hint).toContain('rejected the credentials');
		expect(failure?.error).toContain('HTTP 403');
	});

	it('points at the tailnet name for a 404', () => {
		expect(pollFailure({ lastError: 'HTTP 404: tailnet not found' })?.hint).toContain('tailnet name');
	});

	it('falls back to a generic retry line and trims long errors', () => {
		const failure = pollFailure({ lastError: `dial tcp: connection refused ${'x'.repeat(500)}` });
		expect(failure?.hint).toContain('keep retrying');
		expect(failure?.error.length).toBe(240);
		expect(failure?.error.endsWith('…')).toBe(true);
	});
});

describe('realTime', () => {
	it('treats the zero time as unknown', () => {
		expect(realTime('0001-01-01T00:00:00Z')).toBeNull();
		expect(realTime('')).toBeNull();
		expect(realTime(undefined)).toBeNull();
		expect(realTime('garbage')).toBeNull();
		expect(realTime('2026-10-12T19:00:00Z')?.toISOString()).toBe('2026-10-12T19:00:00.000Z');
	});
});
