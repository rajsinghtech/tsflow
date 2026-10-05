import { describe, expect, it } from 'vitest';
import { deviceScopeLabel, nodeMatchesDeviceScope } from './device-scope';

describe('nodeMatchesDeviceScope', () => {
	it('matches every node when no scope is set', () => {
		expect(nodeMatchesDeviceScope({ user: 'ada@example.com', tags: [] }, null)).toBe(true);
		expect(nodeMatchesDeviceScope({ user: '', tags: [] }, undefined)).toBe(true);
	});

	it('matches the viewer login exactly', () => {
		const scope = { owners: ['Ada@example.com'], tags: [] };
		expect(nodeMatchesDeviceScope({ user: 'ada@example.com', tags: [] }, scope)).toBe(true);
		expect(nodeMatchesDeviceScope({ user: 'ada@example.com.extra', tags: [] }, scope)).toBe(false);
		expect(nodeMatchesDeviceScope({ user: 'other@example.com', tags: ['tag:eng'] }, scope)).toBe(false);
	});

	it('matches mapped group tags and owner logins', () => {
		const scope = { owners: ['ops@example.com'], tags: ['tag:eng'] };
		expect(nodeMatchesDeviceScope({ user: 'someone@example.com', tags: ['tag:eng'] }, scope)).toBe(true);
		expect(nodeMatchesDeviceScope({ user: 'someone@example.com', tags: ['eng'] }, scope)).toBe(true);
		expect(nodeMatchesDeviceScope({ user: 'ops@example.com', tags: [] }, scope)).toBe(true);
		expect(nodeMatchesDeviceScope({ user: 'ada@example.com', tags: ['tag:other'] }, scope)).toBe(false);
		expect(nodeMatchesDeviceScope({ user: 'ada@example.com', tags: ['tag:engineering'] }, scope)).toBe(false);
	});

	it('matches nothing when the scope is empty', () => {
		expect(nodeMatchesDeviceScope({ user: 'ada@example.com', tags: ['tag:eng'] }, { owners: [], tags: [] })).toBe(
			false
		);
	});

	it('labels the chip from owners and tags', () => {
		expect(deviceScopeLabel(null)).toBe('');
		expect(deviceScopeLabel({ owners: [], tags: [] })).toBe('No matching devices');
		expect(deviceScopeLabel({ owners: ['ada@example.com'], tags: ['tag:eng'] })).toBe(
			'ada@example.com, tag:eng'
		);
	});
});
