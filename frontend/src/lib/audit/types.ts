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
	events: AuditCount[];
	actors: AuditCount[];
	targetTypes: AuditCount[];
	origins: AuditCount[];
	total: number;
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
	failed?: boolean;
	q?: string;
	cursor?: string;
	limit?: number;
}
