// A graph node, or the device record hanging off it, can carry the login.
export interface SearchableNode {
	displayName?: string;
	user?: string;
	ips?: string[];
	tags?: string[];
	device?: {
		name?: string;
		hostname?: string;
		user?: string;
	};
}

function logins(node: SearchableNode): string[] {
	const values = [node.user, node.device?.user];
	const seen = new Set<string>();
	const out: string[] = [];
	for (const value of values) {
		const login = value?.trim().toLowerCase();
		if (!login || seen.has(login)) continue;
		seen.add(login);
		out.push(login);
	}
	return out;
}

function loginMatches(node: SearchableNode, query: string): boolean {
	if (!query) return logins(node).length > 0;
	return logins(node).some((login) => login.includes(query));
}

// nodeMatchesSearch is the traffic-graph search box.
// tag: and ip: keep their prefixes. user@term searches the login for term.
// Anything else, including a full or partial email, is a case-insensitive
// substring. '@' and '.' are ordinary characters in that substring.
export function nodeMatchesSearch(node: SearchableNode, query: string): boolean {
	if (!query) return true;
	const q = query.toLowerCase().trim();
	if (!q) return true;

	if (q.startsWith('tag:')) {
		const tagSearch = q.slice(4);
		return (node.tags ?? []).some((tag) => tag.toLowerCase().replace(/^tag:/, '').includes(tagSearch));
	}
	if (q.startsWith('ip:')) {
		const ipSearch = q.slice(3);
		return (node.ips ?? []).some((ip) => ip.toLowerCase().includes(ipSearch));
	}
	if (q.startsWith('user@')) {
		return loginMatches(node, q.slice('user@'.length));
	}

	const matchesIP = (node.ips ?? []).some((ip) => ip.toLowerCase().includes(q));
	const names = [node.displayName, node.device?.name, node.device?.hostname];
	const matchesName = names.some((name) => (name ?? '').toLowerCase().includes(q));
	const matchesTags = (node.tags ?? []).some((tag) => tag.toLowerCase().replace(/^tag:/, '').includes(q));
	return matchesIP || matchesName || loginMatches(node, q) || matchesTags;
}
