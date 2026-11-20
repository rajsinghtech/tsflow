import type { AuditEvent } from './types';

// Friendly names for the events people look for most. Anything else falls
// back to a readable form of TARGET.ACTION.PROPERTY.
const EVENT_LABELS: Record<string, string> = {
	'NODE.LOGIN': 'Device login',
	'NODE.LOGOUT': 'Device logout',
	'NODE.CREATE': 'Device added',
	'NODE.DELETE': 'Device removed',
	'NODE.UPDATE.APPROVAL': 'Device approved',
	'NODE.UPDATE.ACL_TAGS': 'Device tags changed',
	'NODE.UPDATE.ADVERTISED_ROUTES': 'Advertised routes changed',
	'NODE.UPDATE.APPROVED_ROUTES': 'Routes approved',
	'NODE.UPDATE.KEY_EXPIRY_DISABLED': 'Key expiry changed',
	'NODE.UPDATE.KEY_EXPIRY_TIME': 'Key expiry changed',
	'NODE.UPDATE.MACHINE_NAME': 'Device renamed',
	'NODE.UPDATE.EXIT_NODE': 'Exit node changed',
	'ADMIN_CONSOLE.LOGIN': 'Admin console login',
	'ADMIN_CONSOLE.LOGOUT': 'Admin console logout',
	'WEB_INTERFACE.LOGIN': 'Web interface login',
	'TAILNET.UPDATE.ACL': 'Policy file changed',
	'TAILNET.UPDATE.POLICY': 'Policy file changed',
	'USER.CREATE': 'User added',
	'USER.DELETE': 'User removed',
	'USER.SUSPEND': 'User suspended',
	'USER.RESTORE': 'User restored',
	'USER.UPDATE.ROLE': 'User role changed',
	'USER.APPROVE': 'User approved',
	'API_KEY.CREATE': 'API key created',
	'API_KEY.DELETE': 'API key revoked',
	'AUTH_KEY.CREATE': 'Auth key created',
	'AUTH_KEY.DELETE': 'Auth key revoked',
	'OAUTH_CLIENT.CREATE': 'OAuth client created',
	'OAUTH_CLIENT.DELETE': 'OAuth client deleted',
	'INVITE.CREATE': 'Invite sent',
	'TAILNET.UPDATE.DNS_NAMESERVERS': 'DNS nameservers changed',
	'TAILNET.UPDATE.DNS_SEARCH_PATHS': 'DNS search paths changed',
	'TAILNET.UPDATE.MAGIC_DNS': 'MagicDNS changed'
};

function titleCase(text: string): string {
	return text
		.toLowerCase()
		.split(/[_\s]+/)
		.filter(Boolean)
		.map((word, i) => (i === 0 ? word[0].toUpperCase() + word.slice(1) : word))
		.join(' ');
}

export function eventLabel(key: string): string {
	if (EVENT_LABELS[key]) return EVENT_LABELS[key];
	const [target = '', action = '', ...property] = key.split('.');
	const parts = [titleCase(target), action.toLowerCase().replace(/_/g, ' ')];
	if (property.length) parts.push(property.join('.').toLowerCase().replace(/_/g, ' '));
	return parts.filter(Boolean).join(' ');
}

export type EventTone = 'login' | 'create' | 'delete' | 'update' | 'error' | 'other';

export function eventTone(event: Pick<AuditEvent, 'event' | 'action' | 'error'>): EventTone {
	if (event.error) return 'error';
	const action = event.action.toUpperCase();
	if (action === 'LOGIN' || action === 'LOGOUT') return 'login';
	if (action === 'CREATE' || action === 'APPROVE' || action === 'RESTORE') return 'create';
	if (action === 'DELETE' || action === 'SUSPEND' || action === 'REVOKE') return 'delete';
	if (action === 'UPDATE') return 'update';
	return 'other';
}

export const TONE_CLASSES: Record<EventTone, string> = {
	login: 'bg-blue-500/15 text-blue-700 dark:text-blue-300',
	create: 'bg-green-500/15 text-green-700 dark:text-green-300',
	delete: 'bg-red-500/15 text-red-700 dark:text-red-300',
	update: 'bg-amber-500/15 text-amber-800 dark:text-amber-300',
	error: 'bg-red-600/20 text-red-700 dark:text-red-300',
	other: 'bg-secondary text-muted-foreground'
};

export function actorLabel(event: Pick<AuditEvent, 'actor'>): string {
	const actor = event.actor ?? {};
	return actor.loginName || actor.displayName || (actor.tags?.length ? actor.tags.join(', ') : '') || actor.id || actor.type || 'unknown';
}

// targetLabel names what changed. short drops the tailnet suffix from a
// device's MagicDNS name (laptop.tail1234.ts.net -> laptop) for dense lists.
export function targetLabel(event: Pick<AuditEvent, 'target'>, short = false): string {
	const target = event.target ?? {};
	const name = target.name || target.id || titleCase(target.type ?? '') || '';
	if (short && (target.type ?? '').toUpperCase() !== 'TAILNET') return shortHost(name);
	return name;
}

export function shortHost(name: string): string {
	return name.endsWith('.ts.net') && name.indexOf('.') > 0 ? name.slice(0, name.indexOf('.')) : name;
}

// isPolicyEvent is true for edits to the tailnet policy file, whose old and
// new values are the whole file as text.
export function isPolicyEvent(event: Pick<AuditEvent, 'event' | 'target'>): boolean {
	const prop = (event.target?.property ?? '').toUpperCase();
	return event.event.startsWith('TAILNET.UPDATE') && (prop === 'ACL' || prop === 'POLICY' || prop === 'POLICY_FILE');
}

// valueText renders an old or new value for display. Strings show as is so
// a policy file keeps its comments; anything else is pretty JSON.
export function valueText(value: unknown): string {
	if (value === undefined || value === null) return '';
	if (typeof value === 'string') return value;
	return JSON.stringify(value, null, 2);
}

export function hasChange(event: Pick<AuditEvent, 'old' | 'new'>): boolean {
	return valueText(event.old) !== '' || valueText(event.new) !== '';
}

export type DiffKind = 'same' | 'add' | 'del';
export interface DiffLine {
	kind: DiffKind;
	text: string;
	oldNo?: number;
	newNo?: number;
}

const MAX_DIFF_CELLS = 9_000_000;

// lineDiff is a line-level LCS diff. Common leading and trailing lines are
// trimmed first, so a one-line edit to a long policy file stays cheap. If
// what is left is still too big, it shows a plain remove-then-add.
export function lineDiff(before: string, after: string): DiffLine[] {
	const a = before === '' ? [] : before.replace(/\r\n/g, '\n').split('\n');
	const b = after === '' ? [] : after.replace(/\r\n/g, '\n').split('\n');
	let head = 0;
	while (head < a.length && head < b.length && a[head] === b[head]) head++;
	let tail = 0;
	while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++;

	const out: DiffLine[] = [];
	for (let i = 0; i < head; i++) out.push({ kind: 'same', text: a[i], oldNo: i + 1, newNo: i + 1 });

	const midA = a.slice(head, a.length - tail);
	const midB = b.slice(head, b.length - tail);
	const n = midA.length;
	const m = midB.length;
	let oi = head;
	let ni = head;
	if (n * m > MAX_DIFF_CELLS) {
		for (const text of midA) out.push({ kind: 'del', text, oldNo: ++oi });
		for (const text of midB) out.push({ kind: 'add', text, newNo: ++ni });
	} else {
		const width = m + 1;
		const table = new Uint32Array((n + 1) * width);
		for (let i = n - 1; i >= 0; i--) {
			for (let j = m - 1; j >= 0; j--) {
				table[i * width + j] =
					midA[i] === midB[j] ? table[(i + 1) * width + j + 1] + 1 : Math.max(table[(i + 1) * width + j], table[i * width + j + 1]);
			}
		}
		let i = 0;
		let j = 0;
		while (i < n && j < m) {
			if (midA[i] === midB[j]) {
				out.push({ kind: 'same', text: midA[i], oldNo: ++oi, newNo: ++ni });
				i++;
				j++;
			} else if (table[(i + 1) * width + j] >= table[i * width + j + 1]) {
				out.push({ kind: 'del', text: midA[i++], oldNo: ++oi });
			} else {
				out.push({ kind: 'add', text: midB[j++], newNo: ++ni });
			}
		}
		while (i < n) out.push({ kind: 'del', text: midA[i++], oldNo: ++oi });
		while (j < m) out.push({ kind: 'add', text: midB[j++], newNo: ++ni });
	}
	for (let k = a.length - tail; k < a.length; k++) out.push({ kind: 'same', text: a[k], oldNo: ++oi, newNo: ++ni });
	return out;
}

export type DiffChunk = { kind: 'lines'; lines: DiffLine[] } | { kind: 'skip'; count: number };

// diffChunks keeps `context` unchanged lines around each change and folds
// the rest into skip markers.
export function diffChunks(lines: DiffLine[], context = 3): DiffChunk[] {
	const keep = new Uint8Array(lines.length);
	lines.forEach((line, idx) => {
		if (line.kind === 'same') return;
		for (let k = Math.max(0, idx - context); k <= Math.min(lines.length - 1, idx + context); k++) keep[k] = 1;
	});
	const chunks: DiffChunk[] = [];
	let current: DiffLine[] = [];
	let skipped = 0;
	lines.forEach((line, idx) => {
		if (keep[idx]) {
			if (skipped) {
				chunks.push({ kind: 'skip', count: skipped });
				skipped = 0;
			}
			current.push(line);
		} else {
			if (current.length) {
				chunks.push({ kind: 'lines', lines: current });
				current = [];
			}
			skipped++;
		}
	});
	if (current.length) chunks.push({ kind: 'lines', lines: current });
	if (skipped) chunks.push({ kind: 'skip', count: skipped });
	return chunks;
}

export function diffStats(lines: DiffLine[]): { added: number; removed: number } {
	let added = 0;
	let removed = 0;
	for (const line of lines) {
		if (line.kind === 'add') added++;
		else if (line.kind === 'del') removed++;
	}
	return { added, removed };
}

export function auditTime(value: string | undefined | null): string {
	if (!value) return '—';
	const parsed = new Date(value);
	if (Number.isNaN(parsed.getTime())) return value;
	return parsed.toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

export function relativeTime(value: string | undefined | null, now: Date = new Date()): string {
	if (!value) return 'never';
	const parsed = new Date(value);
	if (Number.isNaN(parsed.getTime())) return value;
	const seconds = Math.round((now.getTime() - parsed.getTime()) / 1000);
	if (seconds < 0) return 'just now';
	if (seconds < 60) return `${seconds}s ago`;
	const minutes = Math.round(seconds / 60);
	if (minutes < 60) return `${minutes}m ago`;
	const hours = Math.round(minutes / 60);
	if (hours < 48) return `${hours}h ago`;
	const days = Math.round(hours / 24);
	return `${days}d ago`;
}

// flowLink opens the traffic page on a device for an hour either side of
// the audit event, so a login can be lined up with what the device did.
export function flowLink(nodeId: string, eventTime: string, padMinutes = 60): string {
	const at = new Date(eventTime).getTime();
	const params = new URLSearchParams({
		device: nodeId,
		from: new Date(at - padMinutes * 60_000).toISOString(),
		to: new Date(at + padMinutes * 60_000).toISOString()
	});
	return `/?${params.toString()}`;
}

export function isNodeTarget(event: Pick<AuditEvent, 'target'>): boolean {
	return (event.target?.type ?? '').toUpperCase() === 'NODE' && !!event.target?.id;
}

// userLogin is the login a user page should open for this event: the actor
// when a person made the change, else the target when it is a user.
export function userLogin(event: Pick<AuditEvent, 'actor' | 'target'>): string | null {
	if (event.actor?.loginName) return event.actor.loginName;
	if ((event.target?.type ?? '').toUpperCase() === 'USER' && event.target?.name) return event.target.name;
	return null;
}
