import { describe, expect, it } from 'vitest';
import { auditSearch, tzOffsetSeconds } from './audit-service';

describe('auditSearch', () => {
	it('repeats list keys and drops blanks', () => {
		expect(auditSearch({ since: '7d', event: ['NODE.LOGIN', ' ', 'ADMIN_CONSOLE.LOGIN'], actor: ['~ada'], failed: true })).toBe(
			'?since=7d&actor=%7Eada&event=NODE.LOGIN&event=ADMIN_CONSOLE.LOGIN&failed=true'
		);
	});
	it('prefers an explicit start over since', () => {
		expect(auditSearch({ since: '7d', start: '2026-01-01T00:00:00Z' })).toBe('?start=2026-01-01T00%3A00%3A00Z');
		expect(auditSearch({})).toBe('');
	});
	it('sends category, actor type and ephemeral filters', () => {
		expect(auditSearch({ category: ['keys', 'policy'], actorType: ['OAUTH_CLIENT'], ephemeral: true, origin: ['CONFIG_API'] })).toBe(
			'?origin=CONFIG_API&category=keys&category=policy&actorType=OAUTH_CLIENT&ephemeral=true'
		);
	});
});

describe('tzOffsetSeconds', () => {
	it('is seconds east of UTC', () => {
		const date = { getTimezoneOffset: () => 360 } as Date;
		expect(tzOffsetSeconds(date)).toBe(-21600);
	});
});
