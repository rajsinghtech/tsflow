import { describe, expect, it } from 'vitest';
import { MIN_WINDOW_MS } from './time-window';
import {
	decodeSearch,
	encodeIntent,
	intentLabel,
	parseRangeInput,
	type RangeIntent
} from './time-range-url';

const now = new Date('2026-10-07T15:00:00.000Z');

function roundTrip(intent: RangeIntent) {
	const encoded = encodeIntent(intent);
	const params = new URLSearchParams(encoded);
	return decodeSearch(params, now);
}

describe('range URL round-trip', () => {
	it('stores a live window as from=now-2h&to=now', () => {
		const encoded = encodeIntent({ kind: 'sliding', windowMs: 2 * 60 * 60 * 1000 });
		expect(encoded).toEqual({ from: 'now-2h', to: 'now' });
		expect(roundTrip({ kind: 'sliding', windowMs: 2 * 60 * 60 * 1000 })).toEqual({
			status: 'ok',
			intent: { kind: 'sliding', windowMs: 2 * 60 * 60 * 1000 }
		});
	});

	it('stores all coverage as a live range', () => {
		expect(encodeIntent({ kind: 'all' })).toEqual({ from: 'all', to: 'now' });
		expect(roundTrip({ kind: 'all' }).status).toBe('ok');
	});

	it('stores a pinned range as ISO instants', () => {
		const start = new Date('2026-10-06T18:00:00.000Z');
		const end = new Date('2026-10-06T20:00:00.000Z');
		const decoded = roundTrip({ kind: 'absolute', start, end });
		expect(decoded.status).toBe('ok');
		if (decoded.status !== 'ok' || decoded.intent.kind !== 'absolute') throw new Error('expected absolute');
		expect(decoded.intent.start.toISOString()).toBe(start.toISOString());
		expect(decoded.intent.end.toISOString()).toBe(end.toISOString());
	});

	it('keeps a fixed start live when to=now', () => {
		const start = new Date('2026-10-07T09:00:00.000Z');
		const decoded = roundTrip({ kind: 'since', start });
		expect(decoded).toEqual({ status: 'ok', intent: { kind: 'since', start } });
	});

	it('reads legacy start and end query params', () => {
		const params = new URLSearchParams({
			start: '2026-10-06T18:00:00.000Z',
			end: '2026-10-06T20:00:00.000Z',
			tailnet: 'lab'
		});
		const decoded = decodeSearch(params, now);
		expect(decoded.status).toBe('ok');
		if (decoded.status !== 'ok' || decoded.intent.kind !== 'absolute') throw new Error('expected absolute');
		expect(decoded.intent.start.toISOString()).toBe('2026-10-06T18:00:00.000Z');
		expect(decoded.intent.end.toISOString()).toBe('2026-10-06T20:00:00.000Z');
	});

	it('prefers from and to over legacy params', () => {
		const params = new URLSearchParams({
			from: 'now-15m',
			to: 'now',
			start: '2026-10-06T18:00:00.000Z',
			end: '2026-10-06T20:00:00.000Z'
		});
		expect(decodeSearch(params, now)).toEqual({
			status: 'ok',
			intent: { kind: 'sliding', windowMs: 15 * 60 * 1000 }
		});
	});

	it('treats a relative end as pinned', () => {
		const params = new URLSearchParams({ from: 'now-3h', to: 'now-1h' });
		const decoded = decodeSearch(params, now);
		expect(decoded.status).toBe('ok');
		if (decoded.status !== 'ok' || decoded.intent.kind !== 'absolute') throw new Error('expected absolute');
		expect(decoded.intent.end.getTime() - decoded.intent.start.getTime()).toBe(2 * 60 * 60 * 1000);
		expect(decoded.intent.end.toISOString()).toBe('2026-10-07T14:00:00.000Z');
	});
});

describe('range parser', () => {
	it('parses durations and prose', () => {
		expect(parseRangeInput('2h', now, 'utc')).toEqual({
			ok: true,
			intent: { kind: 'sliding', windowMs: 2 * 60 * 60 * 1000 }
		});
		expect(parseRangeInput('last 3 hours', now, 'utc')).toEqual({
			ok: true,
			intent: { kind: 'sliding', windowMs: 3 * 60 * 60 * 1000 }
		});
		expect(parseRangeInput('now-15m', now, 'utc')).toEqual({
			ok: true,
			intent: { kind: 'sliding', windowMs: 15 * 60 * 1000 }
		});
		expect(parseRangeInput('7d', now, 'utc')).toEqual({
			ok: true,
			intent: { kind: 'sliding', windowMs: 7 * 24 * 60 * 60 * 1000 }
		});
	});

	it('parses since 9am in the chosen zone', () => {
		const parsed = parseRangeInput('since 9am', now, 'utc');
		expect(parsed.ok).toBe(true);
		if (!parsed.ok || parsed.intent.kind !== 'since') throw new Error('expected since');
		expect(parsed.intent.start.toISOString()).toBe('2026-10-07T09:00:00.000Z');
		expect(intentLabel(parsed.intent, 'utc')).toBe('Since 9:00 AM');
	});

	it('rolls a future clock back a day', () => {
		const morning = new Date('2026-10-07T08:00:00.000Z');
		const parsed = parseRangeInput('since 9am', morning, 'utc');
		if (!parsed.ok || parsed.intent.kind !== 'since') throw new Error('expected since');
		expect(parsed.intent.start.toISOString()).toBe('2026-10-06T09:00:00.000Z');
	});

	it('parses a clock range as pinned', () => {
		const parsed = parseRangeInput('14:00-15:30', now, 'utc');
		expect(parsed.ok).toBe(true);
		if (!parsed.ok || parsed.intent.kind !== 'absolute') throw new Error('expected absolute');
		expect(parsed.intent.start.toISOString()).toBe('2026-10-07T14:00:00.000Z');
		expect(parsed.intent.end.toISOString()).toBe('2026-10-07T15:30:00.000Z');
	});

	it('parses ISO and unix instants', () => {
		const iso = parseRangeInput('2026-10-06T18:00:00.000Z to 2026-10-06T20:00:00.000Z', now, 'utc');
		if (!iso.ok || iso.intent.kind !== 'absolute') throw new Error('expected absolute');
		expect(iso.intent.end.getTime() - iso.intent.start.getTime()).toBe(2 * 60 * 60 * 1000);

		const unix = parseRangeInput('1710000000-1710003600', now, 'utc');
		if (!unix.ok || unix.intent.kind !== 'absolute') throw new Error('expected absolute');
		expect(unix.intent.start.toISOString()).toBe('2024-03-09T16:00:00.000Z');
		expect(unix.intent.end.getTime() - unix.intent.start.getTime()).toBe(60 * 60 * 1000);
	});

	it('rejects empty, tiny, and unknown text', () => {
		expect(parseRangeInput('   ', now, 'utc').ok).toBe(false);
		expect(parseRangeInput('1m', now, 'utc')).toMatchObject({ ok: false, message: expect.stringMatching(/5 minutes/) });
		expect(parseRangeInput('banana', now, 'utc')).toMatchObject({ ok: false });
		expect(MIN_WINDOW_MS).toBe(5 * 60 * 1000);
	});
});
