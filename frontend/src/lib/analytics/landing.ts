export const LAST_TAB_KEY = 'tsflow-last-tab';

export const KNOWN_TABS = ['/', '/me', '/analytics', '/new', '/policy'] as const;

export function showMeTab(login: string | null | undefined): boolean {
	return !!login?.trim();
}

// landingTarget is where a visit to the traffic page should go.
// No login keeps today's traffic page. A query string is a deep link and stays.
// A remembered tab wins. Otherwise a known login opens Me.
export function landingTarget(input: {
	login: string | null | undefined;
	pathname: string;
	search: string;
	stored: string | null;
}): string | null {
	if (!showMeTab(input.login)) return null;
	if (input.pathname !== '/') return null;
	if (input.search && input.search !== '?') return null;
	const stored = input.stored?.trim() ?? '';
	if (stored === '/') return null;
	if ((KNOWN_TABS as readonly string[]).includes(stored)) return stored;
	return '/me';
}

export function readLastTab(): string | null {
	if (typeof localStorage === 'undefined') return null;
	return localStorage.getItem(LAST_TAB_KEY);
}

export function rememberTab(pathname: string): void {
	if (typeof localStorage === 'undefined') return;
	if (!(KNOWN_TABS as readonly string[]).includes(pathname)) return;
	localStorage.setItem(LAST_TAB_KEY, pathname);
}
