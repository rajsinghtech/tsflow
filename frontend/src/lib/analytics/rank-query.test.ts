import { describe, expect, it } from 'vitest';
import { pageStep, rankNodeLabel, rankPageLabel, rankQueryPath } from './rank-query';

const start = new Date('2026-03-01T12:00:00.000Z');
const end = new Date('2026-03-01T14:00:00.000Z');

describe('rank query paths', () => {
	it('builds talker and pair urls without a tailnet parameter', () => {
		expect(rankQueryPath('talkers', { start, end, limit: 20, offset: 0, sort: 'bytes' })).toBe(
			'/analytics/talkers?start=2026-03-01T12:00:00.000Z&end=2026-03-01T14:00:00.000Z&limit=20&offset=0&sort=bytes'
		);
		expect(rankQueryPath('pairs', { start, end, limit: 20, offset: 40, sort: 'flows' })).toBe(
			'/analytics/pairs?start=2026-03-01T12:00:00.000Z&end=2026-03-01T14:00:00.000Z&limit=20&offset=40&sort=flows'
		);
		expect(rankQueryPath('talkers', { start, end }).includes('tailnet=')).toBe(false);
	});

	it('defaults limit, offset, and sort', () => {
		const path = rankQueryPath('talkers', { start, end });
		expect(path).toContain('limit=20');
		expect(path).toContain('offset=0');
		expect(path).toContain('sort=bytes');
	});
});

describe('rank pages', () => {
	it('steps forward only when another page exists', () => {
		expect(pageStep(0, 20, 1, true)).toBe(20);
		expect(pageStep(0, 20, 1, false)).toBeNull();
		expect(pageStep(20, 20, 1, true)).toBe(40);
	});

	it('steps backward without passing zero', () => {
		expect(pageStep(0, 20, -1, true)).toBeNull();
		expect(pageStep(40, 20, -1, false)).toBe(20);
		expect(pageStep(10, 20, -1, true)).toBe(0);
	});

	it('labels the visible row range', () => {
		expect(rankPageLabel(0, 0)).toBe('No rows');
		expect(rankPageLabel(0, 1)).toBe('1');
		expect(rankPageLabel(20, 20)).toBe('21-40');
	});
});

describe('rank node labels', () => {
	it('prefers a hostname and shortens a bare numeric id', () => {
		expect(rankNodeLabel('laptop', 'a')).toEqual({ text: 'laptop', mono: false });
		expect(rankNodeLabel('  ', '12345678901')).toEqual({ text: '12345678\u2026', mono: true });
		expect(rankNodeLabel('', 'node-a')).toEqual({ text: 'node-a', mono: true });
	});
});
