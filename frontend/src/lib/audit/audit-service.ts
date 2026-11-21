import { api, apiUrl } from '#lib/services/api-service';
import { setCatalog } from './format';
import type {
	AuditCatalog,
	AuditEvent,
	AuditFacets,
	AuditInsights,
	AuditNodeResponse,
	AuditPage,
	AuditQuery,
	AuditStatus,
	AuditUserResponse
} from './types';

// auditSearch builds the query string for /api/audit/events and /facets.
// List values repeat the key, which the backend reads as OR.
export function auditSearch(query: AuditQuery): string {
	const params = new URLSearchParams();
	const add = (key: string, values: string[] | undefined) => {
		for (const value of values ?? []) {
			const trimmed = value.trim();
			if (trimmed) params.append(key, trimmed);
		}
	};
	if (query.start) params.set('start', query.start);
	if (query.end) params.set('end', query.end);
	if (!query.start && query.since) params.set('since', query.since);
	add('actor', query.actor);
	add('target', query.target);
	add('targetId', query.targetId);
	if (query.user?.trim()) params.set('user', query.user.trim());
	add('event', query.event);
	add('origin', query.origin);
	add('targetType', query.targetType);
	add('category', query.category);
	add('actorType', query.actorType);
	if (query.ephemeral) params.set('ephemeral', 'true');
	if (query.failed) params.set('failed', 'true');
	if (query.q?.trim()) params.set('q', query.q.trim());
	if (query.cursor) params.set('cursor', query.cursor);
	if (query.limit) params.set('limit', String(query.limit));
	const text = params.toString();
	return text ? `?${text}` : '';
}

export const auditService = {
	status: (signal?: AbortSignal) => api.get<AuditStatus>('/audit/status', { signal }),
	events: (query: AuditQuery, signal?: AbortSignal) => api.get<AuditPage>(`/audit/events${auditSearch(query)}`, { signal }),
	facets: (query: AuditQuery, signal?: AbortSignal) =>
		api.get<AuditFacets>(`/audit/facets${auditSearch({ ...query, cursor: undefined, limit: undefined })}`, { signal }),
	event: (id: number, signal?: AbortSignal) =>
		api.get<{ event: AuditEvent; group: AuditEvent[] }>(`/audit/events/${id}`, { signal }),
	user: (login: string, query: { since?: string; cursor?: string } = {}, signal?: AbortSignal) =>
		api.get<AuditUserResponse>(`/audit/users/${encodeURIComponent(login)}${auditSearch(query)}`, { signal }),
	node: (nodeId: string, query: { since?: string; limit?: number } = {}, signal?: AbortSignal) =>
		api.get<AuditNodeResponse>(`/audit/nodes/${encodeURIComponent(nodeId)}${auditSearch(query)}`, { signal }),
	catalog: (signal?: AbortSignal) => api.get<AuditCatalog>('/audit/catalog', { signal }),
	insights: (query: AuditQuery, signal?: AbortSignal) =>
		api.get<AuditInsights>(`/audit/insights${withParam(auditSearch({ ...query, cursor: undefined, limit: undefined }), 'tzOffset', String(tzOffsetSeconds()))}`, {
			signal
		}),
	// exportUrl is a download link for the current filters (CSV or NDJSON).
	exportUrl: (query: AuditQuery, format: 'csv' | 'ndjson') =>
		apiUrl(`/audit/export${withParam(auditSearch({ ...query, cursor: undefined, limit: undefined }), 'format', format)}`)
};

// tzOffsetSeconds is the browser's offset east of UTC, so day buckets start
// at local midnight.
export function tzOffsetSeconds(date: Date = new Date()): number {
	return -date.getTimezoneOffset() * 60;
}

function withParam(search: string, key: string, value: string): string {
	const join = search ? '&' : '?';
	return `${search}${join}${key}=${encodeURIComponent(value)}`;
}

let catalogLoad: Promise<AuditCatalog | null> | null = null;

// loadCatalog fetches the event catalog once per page load and installs its
// labels. It resolves to null (keeping the built-in labels) on failure.
export function loadCatalog(): Promise<AuditCatalog | null> {
	catalogLoad ??= auditService
		.catalog()
		.then((catalog) => {
			setCatalog(catalog);
			return catalog;
		})
		.catch(() => {
			catalogLoad = null;
			return null;
		});
	return catalogLoad;
}
