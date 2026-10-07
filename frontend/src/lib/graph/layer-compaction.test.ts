import { describe, expect, it } from 'vitest';
import { compactBands, compactLayers, type Box } from './layer-compaction';

function overlaps(a: Box, b: Box): boolean {
	return a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
}

describe('layer compaction', () => {
	it('squeezes a spread layout to the widest band without overlaps or reordering', () => {
		// A hub on top, three leaves below, and a lone node pushed far right.
		const boxes: Box[] = [
			{ x: 5000, y: 0, width: 200, height: 80 },
			{ x: 0, y: 300, width: 200, height: 80 },
			{ x: 3000, y: 300, width: 200, height: 100 },
			{ x: 9000, y: 310, width: 200, height: 80 },
			{ x: 12000, y: 600, width: 200, height: 80 }
		];
		const xs = compactLayers(boxes, 40);
		const placed = boxes.map((box, i) => ({ ...box, x: xs[i] }));
		const width = Math.max(...placed.map((b) => b.x + b.width)) - Math.min(...placed.map((b) => b.x));
		expect(width).toBeLessThanOrEqual(3 * 200 + 2 * 40);
		for (let i = 0; i < placed.length; i++) {
			for (let j = i + 1; j < placed.length; j++) {
				expect(overlaps(placed[i], placed[j]), `${i} and ${j} overlap`).toBe(false);
			}
		}
		// The leaves keep their left-to-right order.
		expect(xs[1]).toBeLessThan(xs[2]);
		expect(xs[2]).toBeLessThan(xs[3]);
		// Vertical positions are untouched.
		expect(placed.map((b) => b.y)).toEqual(boxes.map((b) => b.y));
	});

	it('leaves a layout that is already compact alone', () => {
		const boxes: Box[] = [
			{ x: 0, y: 0, width: 200, height: 80 },
			{ x: 240, y: 0, width: 200, height: 80 },
			{ x: 100, y: 300, width: 200, height: 80 }
		];
		expect(compactLayers(boxes, 40)).toEqual([0, 240, 100]);
	});
});

describe('band compaction', () => {
	const boxes: Box[] = [
		{ x: 0, y: 0, width: 200, height: 80 },
		{ x: 300, y: 10, width: 200, height: 80 }, // same band, 10 px lower
		{ x: 0, y: 1000, width: 200, height: 80 }, // 910 px below the first band
		{ x: 0, y: 2990, width: 200, height: 80 } // 1910 px below the second
	];

	it('shrinks gaps in proportion to reach the target height', () => {
		// Bands are 90 + 80 + 80 = 250 tall; 1,000 px leaves 750 for the two gaps.
		const ys = compactBands(boxes, 200, 1000);
		expect(ys[1] - ys[0]).toBe(10);
		expect(ys[3] + 80 - ys[0]).toBeCloseTo(1000, 0);
		// The larger gap stays the larger one.
		expect(ys[3] - (ys[2] + 80)).toBeGreaterThan(ys[2] - 90);
	});

	it('never closes a gap below the layer gap', () => {
		const ys = compactBands(boxes, 200, 300);
		expect(ys[2] - 90).toBe(200);
		expect(ys[3] - (ys[2] + 80)).toBe(200);
	});

	it('leaves a layout within the target alone', () => {
		expect(compactBands(boxes, 200, 5000)).toEqual(boxes.map((b) => b.y));
	});
});
