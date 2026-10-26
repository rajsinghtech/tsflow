import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

// The New tab is gone. Old links and bookmarks land on Analytics and keep
// their query string (tailnet, range), so nothing 404s.
export const prerender = false;

export const load: PageLoad = ({ url }) => {
	redirect(307, `/analytics${url.search}`);
};
