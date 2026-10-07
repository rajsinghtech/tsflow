import { nodeMatchesSearch, type SearchableNode } from './node-search';

export interface IndexedSearchNode extends SearchableNode {
	id: string;
}

interface PreparedNode {
	id: string;
	tags: string[];
	logins: string[];
	names: string[];
	ips: string[];
}

export interface NodeSearchIndex {
	nodes: PreparedNode[];
}

// buildNodeSearchIndex prepares lowercase fields once. A later keystroke scans
// those fields instead of walking every device record again.
export function buildNodeSearchIndex(nodes: readonly IndexedSearchNode[]): NodeSearchIndex {
	return {
		nodes: nodes.map((node) => ({
			id: node.id,
			tags: tagsOf(node),
			logins: loginsOf(node),
			names: namesOf(node),
			ips: (node.ips ?? []).map((ip) => ip.toLowerCase())
		}))
	};
}

// searchNodeIds returns null when the query is empty, which matches every node.
// Otherwise it returns the ids nodeMatchesSearch would accept.
export function searchNodeIds(index: NodeSearchIndex, query: string): Set<string> | null {
	const q = query.toLowerCase().trim();
	if (!q) return null;
	const ids = new Set<string>();
	for (const node of index.nodes) {
		if (preparedMatches(node, q)) ids.add(node.id);
	}
	return ids;
}

function preparedMatches(node: PreparedNode, q: string): boolean {
	if (q.startsWith('tag:')) {
		const tagSearch = q.slice(4);
		if (!tagSearch) return node.tags.length > 0;
		return node.tags.some((tag) => tag.includes(tagSearch));
	}
	if (q.startsWith('ip:')) {
		const ipSearch = q.slice(3);
		return node.ips.some((ip) => ip.includes(ipSearch));
	}
	if (q.startsWith('user@')) {
		return loginMatches(node, q.slice('user@'.length));
	}
	const matchesIP = node.ips.some((ip) => ip.includes(q));
	const matchesName = node.names.some((name) => name.includes(q));
	const matchesTags = node.tags.some((tag) => tag.includes(q));
	return matchesIP || matchesName || loginMatches(node, q) || matchesTags;
}

function loginMatches(node: PreparedNode, query: string): boolean {
	if (!query) return node.logins.length > 0;
	return node.logins.some((login) => login.includes(query));
}

function tagsOf(node: SearchableNode): string[] {
	const out: string[] = [];
	const seen = new Set<string>();
	for (const tag of node.tags ?? []) {
		const name = tag.toLowerCase().replace(/^tag:/, '');
		if (!name || seen.has(name)) continue;
		seen.add(name);
		out.push(name);
	}
	return out;
}

function loginsOf(node: SearchableNode): string[] {
	const out: string[] = [];
	const seen = new Set<string>();
	for (const value of [node.user, node.device?.user]) {
		const login = value?.trim().toLowerCase();
		if (!login || seen.has(login)) continue;
		seen.add(login);
		out.push(login);
	}
	return out;
}

function namesOf(node: SearchableNode): string[] {
	const out: string[] = [];
	const seen = new Set<string>();
	for (const value of [node.displayName, node.device?.name, node.device?.hostname]) {
		const name = value?.trim().toLowerCase();
		if (!name || seen.has(name)) continue;
		seen.add(name);
		out.push(name);
	}
	return out;
}

// indexAgreesWithSearch is used by tests to lock the fast path to the graph matcher.
export function indexAgreesWithSearch(nodes: readonly IndexedSearchNode[], query: string): boolean {
	const ids = searchNodeIds(buildNodeSearchIndex(nodes), query);
	return nodes.every((node) => {
		const want = nodeMatchesSearch(node, query);
		const got = ids === null ? true : ids.has(node.id);
		return want === got;
	});
}
