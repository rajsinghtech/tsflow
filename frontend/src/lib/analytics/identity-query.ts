// IdentityQuery is the tag and login filter shared by the graph search box
// and the analytics rankings. An empty search leaves analytics unfiltered.
export interface IdentityQuery {
	tag?: string;
	user?: string;
	q?: string;
}

export function analyticsIdentityQuery(search: string): IdentityQuery | null {
	const raw = search.trim();
	if (!raw) return null;
	const q = raw.toLowerCase();
	if (q.startsWith('tag:')) {
		const tag = q.slice(4).trim();
		return { tag: tag || '*' };
	}
	if (q.startsWith('ip:')) return null;
	if (q.startsWith('user@')) {
		const user = q.slice('user@'.length).trim();
		return { user: user || '*' };
	}
	if (q.includes('@')) return { user: q };
	return { q };
}

export function describeIdentityQuery(query: IdentityQuery): string {
	if (query.tag === '*') return 'any ACL tag';
	if (query.tag) return `tag:${query.tag}`;
	if (query.user === '*') return 'any login';
	if (query.user) return query.user;
	if (query.q) return `“${query.q}”`;
	return 'this search';
}

export function identityParams(query: IdentityQuery | null): string {
	if (!query) return '';
	const params = new URLSearchParams();
	if (query.tag) params.set('tag', query.tag);
	if (query.user) params.set('user', query.user);
	if (query.q) params.set('q', query.q);
	const encoded = params.toString();
	return encoded ? `&${encoded}` : '';
}
