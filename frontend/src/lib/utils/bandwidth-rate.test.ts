import { describe, expect, it } from 'vitest';
import { bytesPerSecond, coveredBucketSeconds } from './bandwidth-rate';

describe('bandwidth bucket rates', () => {
	it('divides a partial edge bucket by the covered duration', () => {
		const day = 86400;
		const twoHours = 7200;
		expect(coveredBucketSeconds(day, twoHours)).toBe(twoHours);
		expect(bytesPerSecond(1000, day, twoHours)).toBeCloseTo(1000 / twoHours);
		expect(bytesPerSecond(1000, day, twoHours)).toBeGreaterThan(bytesPerSecond(1000, day, day));
	});

	it('uses the nominal bucket length when coverage is absent', () => {
		expect(coveredBucketSeconds(60)).toBe(60);
		expect(bytesPerSecond(120, 60)).toBe(2);
	});
});
