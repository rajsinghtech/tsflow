import { describe, expect, it } from 'vitest';
import { selectedDeviceId, timelineColumns, type TimelineBucket } from './device-timeline';

describe('device timeline', () => {
	it('splits bytes by traffic type and leaves physical out unless present', () => {
		const buckets: TimelineBucket[] = [
			{
				time: '2026-03-02T12:00:00Z',
				virtual: { txBytes: 100, rxBytes: 10 },
				subnet: { txBytes: 5, rxBytes: 40 },
				exit: { txBytes: 7, rxBytes: 1 }
			},
			{
				time: '2026-03-02T13:00:00Z',
				virtual: { txBytes: 0, rxBytes: 0 },
				subnet: { txBytes: 0, rxBytes: 0 },
				exit: { txBytes: 0, rxBytes: 0 },
				physical: { txBytes: 5000, rxBytes: 0 }
			}
		];
		const columns = timelineColumns(buckets);
		expect(columns[0]).toMatchObject({ virtual: 110, subnet: 45, exit: 8, physical: 0, total: 163 });
		expect(columns[1].physical).toBe(5000);
		expect(columns[1].total).toBe(5000);
	});

	it('prefers the selected graph device over a query param', () => {
		expect(selectedDeviceId('from-url', null, [])).toBe('from-url');
		expect(selectedDeviceId('from-url', 'node-1', [{ id: 'node-1', device: { id: 'stable' }, ip: '100.64.0.8' }])).toBe(
			'stable'
		);
		expect(selectedDeviceId(null, 'ext', [{ id: 'ext', ip: '10.1.1.5' }])).toBe('10.1.1.5');
		expect(selectedDeviceId('  ', null, [])).toBeNull();
	});
});
