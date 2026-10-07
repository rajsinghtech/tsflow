import { describe, expect, it } from 'vitest';
import { averageBytesPerNode, headerNodeCount, resolveActiveNodeCount } from './active-nodes';

describe('active node count', () => {
	it('uses the window total instead of a capped top-talkers list', () => {
		const cappedTalkers = 10;
		const totalNodes = 343;
		const totalBytes = 59_000_000_000;

		expect(resolveActiveNodeCount(totalNodes)).toBe(343);
		expect(resolveActiveNodeCount(totalNodes)).not.toBe(cappedTalkers);
		expect(averageBytesPerNode(totalBytes, resolveActiveNodeCount(totalNodes))).toBe(totalBytes / 343);
		expect(averageBytesPerNode(totalBytes, cappedTalkers)).toBe(5_900_000_000);
	});

	it('does not treat a missing count as the length of a ranking', () => {
		expect(resolveActiveNodeCount(undefined)).toBe(0);
		expect(resolveActiveNodeCount(null)).toBe(0);
		expect(resolveActiveNodeCount(Number.NaN)).toBe(0);
		expect(resolveActiveNodeCount(-1)).toBe(0);
		expect(headerNodeCount(10, undefined, true, false)).toBe(0);
	});

	it('keeps the traffic graph count and uses overview nodes on analytics', () => {
		expect(headerNodeCount(340, 343, true, true)).toBe(340);
		expect(headerNodeCount(340, 343, true, false)).toBe(343);
		expect(headerNodeCount(0, 343, true, true)).toBe(343);
		expect(headerNodeCount(340, undefined, false, false)).toBe(340);
		expect(headerNodeCount(0, 0, true, false)).toBe(0);
	});

	it('returns zero average when there is no traffic or no nodes', () => {
		expect(averageBytesPerNode(0, 343)).toBe(0);
		expect(averageBytesPerNode(100, 0)).toBe(0);
	});
});
