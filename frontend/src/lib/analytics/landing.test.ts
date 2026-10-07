import { describe, expect, it } from 'vitest';
import { landingTarget, showMeTab } from './landing';

describe('Me tab landing', () => {
	it('hides Me and stays on Traffic when identity is unknown', () => {
		expect(showMeTab(undefined)).toBe(false);
		expect(showMeTab('')).toBe(false);
		expect(showMeTab('   ')).toBe(false);
		expect(
			landingTarget({ login: null, pathname: '/', search: '', stored: '/me' })
		).toBeNull();
		expect(
			landingTarget({ login: '', pathname: '/', search: '', stored: null })
		).toBeNull();
	});

	it('opens Me by default, keeps deep links, and honors the last tab', () => {
		expect(showMeTab('ada@example.com')).toBe(true);
		expect(
			landingTarget({ login: 'ada@example.com', pathname: '/', search: '', stored: null })
		).toBe('/me');
		expect(
			landingTarget({ login: 'ada@example.com', pathname: '/analytics', search: '', stored: null })
		).toBeNull();
		expect(
			landingTarget({ login: 'ada@example.com', pathname: '/', search: '?tailnet=alpha', stored: null })
		).toBeNull();
		expect(
			landingTarget({ login: 'ada@example.com', pathname: '/', search: '', stored: '/' })
		).toBeNull();
		expect(
			landingTarget({ login: 'ada@example.com', pathname: '/', search: '', stored: '/analytics' })
		).toBe('/analytics');
		expect(
			landingTarget({ login: 'ada@example.com', pathname: '/', search: '', stored: '/nope' })
		).toBe('/me');
	});
});