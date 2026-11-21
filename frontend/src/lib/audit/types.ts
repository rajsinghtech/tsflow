// Shapes returned by /api/audit/*. They mirror backend/internal/audit and
// backend/internal/handlers/audit_handlers.go.

export interface AuditActor {
	id?: string;
	type?: string;
	loginName?: string;
	displayName?: string;
	tags?: string[];
}

export interface AuditTarget {
	id?: string;
	name?: string;
	type?: string;
	property?: string;
	isEphemeral?: boolean;
}

export interface AuditEvent {
	id: number;
	tailnet: string;
	eventTime: string;
	eventGroupID?: string;
	origin?: string;
	action: string;
	event: string;
	actor: AuditActor;
	target: AuditTarget;
	old?: unknown;
	new?: unknown;
	actionDetails?: string;
	error?: string;
	// deferredAt is set when Tailscale's rate limiter delayed the record.
	deferredAt?: string;
	category?: string;
}

export interface AuditPage {
	tailnet?: string;
	start?: string;
	end?: string;
	events: AuditEvent[];
	nextCursor?: string;
}

export interface AuditCount {
	value: string;
	label?: string;
	count: number;
}

export interface AuditFacets {
	categories: AuditCount[];
	events: AuditCount[];
	actors: AuditCount[];
	actorTypes: AuditCount[];
	targets: AuditCount[];
	targetTypes: AuditCount[];
	origins: AuditCount[];
	failed: number;
	ephemeral: number;
	total: number;
}

export interface AuditCategory {
	id: string;
	label: string;
}

export interface AuditCatalogEntry {
	event: string;
	label: string;
	category: string;
}

export interface AuditCatalog {
	categories: AuditCategory[];
	events: AuditCatalogEntry[];
}

export interface AuditHistogramBucket {
	start: string;
	total: number;
	failed: number;
	categories: Record<string, number>;
}

export interface AuditHistogram {
	bucket: 'hour' | 'day' | string;
	buckets: AuditHistogramBucket[];
}

// AuditWho is who did something and when, pointing at the event.
export interface AuditWho {
	at: string;
	actor?: string;
	actorType?: string;
	eventId: number;
}

export interface AuditKey {
	id: string;
	name?: string;
	kind?: 'auth_key' | 'oauth_client' | 'federated_identity' | 'api_access_token' | string;
	status: 'active' | 'revoked' | 'expired' | 'unknown' | string;
	created?: AuditWho;
	revoked?: AuditWho;
	expired?: AuditWho;
	updates: number;
	lastEvent: string;
	expires?: string;
	tags?: string[];
	scopes?: string[];
	reusable?: boolean;
	ephemeral?: boolean;
	preauthorized?: boolean;
	description?: string;
	firstSeen?: string;
}

export interface AuditSetting {
	event: string;
	label: string;
	property: string;
	state: string;
	changes: number;
	last?: AuditWho;
	category: string;
	at: string;
}

export interface AuditPolicyChange {
	eventId: number;
	at: string;
	actor: string;
	origin?: string;
	added: number;
	removed: number;
	error?: string;
	details?: string;
}

export interface AuditInsights {
	histogram: AuditHistogram;
	policy: AuditPolicyChange[];
	keys: AuditKey[];
	settings: AuditSetting[];
}

export interface AuditNodeSummary {
	id: string;
	name?: string;
	ephemeral: boolean;
	created?: AuditWho;
	approved?: AuditWho;
	deleted?: AuditWho;
	lastLogin?: AuditWho;
	lastLogout?: AuditWho;
	keyExpiry?: string;
	keyExpiryDisabled?: boolean;
	keyExpired?: AuditWho;
	tags?: unknown;
	tagsChanged?: AuditWho;
	routes?: unknown;
	autoApprovedRoutes?: unknown;
	exitNode?: unknown;
	tailnetLockSigned?: AuditWho;
	postureChanges: number;
	renames: number;
	events: number;
	actors: string[];
}

export interface AuditNodeResponse {
	nodeId: string;
	summary: AuditNodeSummary | null;
	events: AuditEvent[];
	nextCursor?: string;
}

export interface AuditSourceStatus {
	tailnet: string;
	backend: string;
	bucket?: string;
	prefix?: string;
	running: boolean;
	pollInterval?: string;
	lastPoll?: string;
	lastSuccess?: string;
	lastError?: string;
	lastErrorTime?: string;
	polls: number;
	eventsIngested: number;
	objectsIngested: number;
}

export interface AuditStoredStats {
	events: number;
	objects: number;
	oldest?: string;
	newest?: string;
	lastIngest?: string;
	cursor?: string;
}

export interface AuditTailnetStatus {
	id: string;
	displayName: string;
	source: AuditSourceStatus;
	stored: AuditStoredStats;
}

export interface AuditStatus {
	enabled: boolean;
	permitted: boolean;
	retention?: string;
	tailnets: AuditTailnetStatus[];
}

export interface AuditLoginRef {
	eventId: number;
	kind: string;
	eventTime: string;
	target?: string;
	targetId?: string;
	origin?: string;
}

export interface AuditNodeRef {
	id: string;
	name: string;
	lastEvent: string;
	lastLogin?: string;
	events: number;
}

export interface AuditUserSummary {
	login: string;
	displayName?: string;
	lastLogin?: AuditLoginRef;
	lastNodeLogin?: AuditLoginRef;
	lastConsoleLogin?: AuditLoginRef;
	lastWebLogin?: AuditLoginRef;
	lastActivity?: string;
	firstSeen?: string;
	actorEvents: number;
	targetEvents: number;
	nodes: AuditNodeRef[];
}

export interface AuditLiveUser {
	id?: string;
	displayName?: string;
	role?: string;
	status?: string;
	created?: string;
	lastSeen?: string;
	currentlyConnected: boolean;
	deviceCount: number;
}

export interface AuditLiveDevice {
	id: string;
	name: string;
	os?: string;
	lastSeen?: string;
	connectedToControl: boolean;
	tags?: string[];
}

export interface AuditUserResponse {
	tailnet: string;
	summary: AuditUserSummary;
	live: { available: boolean; error?: string; user?: AuditLiveUser; devices: AuditLiveDevice[] };
	timeline: AuditPage;
}

export interface AuditQuery {
	since?: string;
	start?: string;
	end?: string;
	actor?: string[];
	target?: string[];
	targetId?: string[];
	user?: string;
	event?: string[];
	origin?: string[];
	targetType?: string[];
	category?: string[];
	actorType?: string[];
	ephemeral?: boolean;
	failed?: boolean;
	q?: string;
	cursor?: string;
	limit?: number;
}
