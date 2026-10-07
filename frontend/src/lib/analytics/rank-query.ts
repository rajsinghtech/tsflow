// Query strings for GET /api/analytics/talkers and GET /api/analytics/pairs.
// Tailnet scope is added by the shared API client, and only when more than
// one tailnet is configured.

export const RANK_PAGE_SIZE = 20;

export type RankSort = 'bytes' | 'flows';

export interface RankQueryParams {
	start: Date;
	end: Date;
	limit?: number;
	offset?: number;
	sort?: RankSort;
	// Search text: a name, owner email, address, tag:x, ip:x, or user@x.
	q?: string;
}

export interface RankedTalker {
	nodeId: string;
	hostname: string;
	owner?: string;
	txBytes: number;
	rxBytes: number;
	totalBytes: number;
	flowCount: number;
}

export interface RankedPair {
	srcNodeId: string;
	srcHostname: string;
	srcOwner?: string;
	dstNodeId: string;
	dstHostname: string;
	dstOwner?: string;
	txBytes: number;
	rxBytes: number;
	totalBytes: number;
	flowCount: number;
}

export interface RankMetadata {
	start: string;
	end: string;
	tailnet: string;
	limit: number;
	offset: number;
	count: number;
	hasMore: boolean;
	sort: RankSort | string;
	trafficTypes?: string[];
	q?: string;
}

export function rankQueryPath(resource: 'talkers' | 'pairs', query: RankQueryParams): string {
	const limit = query.limit ?? RANK_PAGE_SIZE;
	const offset = query.offset ?? 0;
	const sort = query.sort ?? 'bytes';
	const q = query.q?.trim() ?? '';
	const search = q ? `&q=${encodeURIComponent(q)}` : '';
	return `/analytics/${resource}?start=${query.start.toISOString()}&end=${query.end.toISOString()}&limit=${limit}&offset=${offset}&sort=${sort}${search}`;
}

// trafficSearchFor is the traffic-graph search that finds a ranked node: its
// name when the device is known, otherwise the stored id (an address).
export function trafficSearchFor(hostname: string | undefined, nodeId: string): string {
	const name = hostname?.trim() ?? '';
	return name || nodeId;
}

// Previous or next page offset. Null means that direction is not available.
export function pageStep(offset: number, limit: number, direction: -1 | 1, hasMore: boolean): number | null {
	const size = limit > 0 ? limit : RANK_PAGE_SIZE;
	if (direction < 0) {
		if (offset <= 0) return null;
		return Math.max(0, offset - size);
	}
	if (!hasMore) return null;
	return offset + size;
}

export function rankPageLabel(offset: number, count: number): string {
	if (count <= 0) return 'No rows';
	const from = offset + 1;
	const to = offset + count;
	if (from === to) return String(from);
	return `${from}-${to}`;
}

export function rankNodeLabel(hostname: string | undefined, nodeId: string): { text: string; mono: boolean } {
	const name = hostname?.trim() ?? '';
	if (name) return { text: name, mono: false };
	if (/^\d{10,}$/.test(nodeId)) return { text: `${nodeId.slice(0, 8)}\u2026`, mono: true };
	return { text: nodeId, mono: true };
}
