import { describe, expect, it } from 'vitest';
import { bandwidthPlotPoints, fillBandwidthGaps, type BandwidthPoint } from './bandwidth-series';

const at = (iso: string, tx: number, seconds = 3600): BandwidthPoint => ({ time: new Date(iso), txBytes: tx, rxBytes: 0, seconds });

describe('fillBandwidthGaps', () => {
	it('drops to zero for hours with no traffic instead of joining across them', () => {
		const filled = fillBandwidthGaps(
			[at('2026-10-25T21:00:00Z', 100), at('2026-10-25T23:00:00Z', 100)],
			new Date('2026-10-25T21:00:00Z'),
			new Date('2026-10-26T00:00:00Z'),
			3600
		);
		expect(filled.map((p) => [p.time.toISOString(), p.txBytes])).toEqual([
			['2026-10-25T21:00:00.000Z', 100],
			['2026-10-25T22:00:00.000Z', 0],
			['2026-10-25T23:00:00.000Z', 100]
		]);
	});

	it('fills a quiet start of the window and keeps off-grid points', () => {
		const filled = fillBandwidthGaps(
			[at('2026-10-25T22:00:00Z', 5), at('2026-10-25T22:30:00Z', 7)],
			new Date('2026-10-25T21:00:00Z'),
			new Date('2026-10-25T23:00:00Z'),
			3600
		);
		expect(filled.map((p) => p.txBytes)).toEqual([0, 5, 7]);
	});

	it('leaves very dense windows alone', () => {
		const points = [at('2026-01-01T00:00:00Z', 1, 60)];
		expect(fillBandwidthGaps(points, new Date('2026-01-01T00:00:00Z'), new Date('2026-02-01T00:00:00Z'), 60)).toEqual(points);
	});
});

describe('bandwidthPlotPoints', () => {
	it('gives the last bucket width up to the window end', () => {
		const end = new Date('2026-10-25T23:30:00Z');
		const plotted = bandwidthPlotPoints([at('2026-10-25T23:00:00Z', 100)], end);
		expect(plotted.map((p) => p.time.toISOString())).toEqual(['2026-10-25T23:00:00.000Z', '2026-10-25T23:30:00.000Z']);
		expect(plotted[1].txBytes).toBe(100);
	});
});
