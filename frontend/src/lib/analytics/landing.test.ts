import { describe, expect, it } from 'vitest';
import { KNOWN_TABS, landingTarget, meAccess, showMeTab } from './landing';

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

	it('treats a remembered New tab as no choice, now that the tab is gone', () => {
		expect(KNOWN_TABS).not.toContain('/new');
		expect(
			landingTarget({ login: 'ada@example.com', pathname: '/', search: '', stored: '/new' })
		).toBe('/me');
		expect(landingTarget({ login: null, pathname: '/', search: '', stored: '/new' })).toBeNull();
	});

	it('keeps the Me page waiting for whoami, then shows it or sends the viewer to Traffic', () => {
		expect(meAccess(false, undefined)).toBe('wait');
		expect(meAccess(false, 'ada@example.com')).toBe('wait');
		expect(meAccess(true, 'ada@example.com')).toBe('show');
		expect(meAccess(true, null)).toBe('redirect');
		expect(meAccess(true, '  ')).toBe('redirect');
	});
});

describe('old New tab links', () => {
	async function target(href: string): Promise<{ status: number; location: string }> {
		const { load } = await import('../../routes/new/+page');
		try {
			await load({ url: new URL(href) } as Parameters<typeof load>[0]);
		} catch (err) {
			return err as { status: number; location: string };
		}
		throw new Error('expected a redirect');
	}

	it('sends /new to Analytics and keeps the query string', async () => {
		expect(await target('http://tsflow.test/new?tailnet=lab&start=1&end=2')).toMatchObject({
			status: 307,
			location: '/analytics?tailnet=lab&start=1&end=2'
		});
		expect(await target('http://tsflow.test/new')).toMatchObject({ location: '/analytics' });
	});
});
