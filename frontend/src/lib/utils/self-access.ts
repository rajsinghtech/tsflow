import type { AccessEdgeKind, AccessEdgeMeta, GraphEdge, GraphNode } from '#lib/policy-engine/types';

const SELF_ACCESS_TYPES = new Set<AccessEdgeKind>(['grant', 'acl', 'ssh']);
const ALL_PORTS = 'all ports and protocols';
const MAX_TOOLTIP_SPECS = 8;

export interface SelfAccessSummary {
	edgeIds: string[];
	/** Port, protocol, and SSH details in display order. */
	constraints: string[];
	tooltip: string;
	ariaLabel: string;
}

interface KindBucket {
	kind: AccessEdgeKind;
	specs: string[];
}

interface SshUsers {
	open: boolean;
	users: string[];
}

function edgeMeta(edge: GraphEdge): AccessEdgeMeta | undefined {
	const meta = edge.meta;
	if (!meta || typeof meta !== 'object') return undefined;
	return meta as AccessEdgeMeta;
}

function pushUnique(list: string[], value: string): void {
	if (!list.includes(value)) list.push(value);
}

function noteSpec(bucket: KindBucket, spec: string): void {
	if (bucket.specs.includes(ALL_PORTS)) return;
	if (spec === ALL_PORTS) {
		bucket.specs = [ALL_PORTS];
		return;
	}
	pushUnique(bucket.specs, spec);
}

function addGrant(bucket: KindBucket, meta: AccessEdgeMeta | undefined): void {
	const ip = (meta?.ip ?? []).map((spec) => spec.trim()).filter(Boolean);
	if (ip.length === 0) return;
	if (ip.some((spec) => spec === '*')) {
		noteSpec(bucket, ALL_PORTS);
		return;
	}
	for (const spec of ip) noteSpec(bucket, spec);
}

function addAcl(bucket: KindBucket, meta: AccessEdgeMeta | undefined): void {
	const proto = meta?.proto?.trim() ?? '';
	const rawPorts = (meta?.ports ?? []).map((port) => port.trim()).filter(Boolean);
	const protoOpen = proto === '' || proto === '*';
	const invalid = rawPorts.includes('__invalid__');
	const starPort = rawPorts.includes('*');
	const ports = rawPorts.filter((port) => port !== '*' && port !== '__invalid__');

	if (invalid && ports.length === 0 && !starPort) {
		noteSpec(bucket, protoOpen ? 'invalid port' : `${proto}:invalid port`);
		return;
	}
	if (starPort || ports.length === 0) {
		noteSpec(bucket, protoOpen ? ALL_PORTS : proto);
		return;
	}
	const portText = ports.join(',');
	if (protoOpen) {
		noteSpec(bucket, ports.length === 1 ? `port ${ports[0]}` : `ports ${portText}`);
		return;
	}
	noteSpec(bucket, `${proto}:${portText}`);
}

function addSshUser(state: SshUsers, meta: AccessEdgeMeta | undefined): void {
	const users = (meta?.sshUsers ?? []).map((user) => user.trim()).filter((user) => user && user !== '*');
	if (users.length === 0) {
		state.open = true;
		return;
	}
	for (const user of users) pushUnique(state.users, user);
}

function sshSpecs(state: SshUsers): string[] {
	if (state.users.length > 0) return [`ssh as ${state.users.join(', ')}`];
	if (state.open) return ['ssh'];
	return [];
}

function baseSentence(nodeType: string): string {
	switch (nodeType) {
		case 'tag':
			return 'This tag can reach other devices with the same tag.';
		case 'group':
			return 'Members of this group can reach each other.';
		case 'autogroup':
			return 'Members of this autogroup can reach each other.';
		default:
			return 'Devices matched by this selector can reach each other.';
	}
}

function constraintClause(buckets: KindBucket[]): { clause: string; constraints: string[] } {
	const active = buckets.filter((bucket) => bucket.specs.length > 0);
	if (active.length === 0) return { clause: '', constraints: [] };

	const multi = active.length > 1;
	const constraints = active.flatMap((bucket) => bucket.specs);
	let remaining = MAX_TOOLTIP_SPECS;
	let omitted = 0;
	const parts: string[] = [];

	for (const bucket of active) {
		if (remaining <= 0) {
			omitted += bucket.specs.length;
			continue;
		}
		const taken = bucket.specs.slice(0, remaining);
		remaining -= taken.length;
		omitted += bucket.specs.length - taken.length;
		const body = taken.join(', ');
		parts.push(multi && bucket.kind !== 'ssh' ? `${bucket.kind}: ${body}` : body);
	}
	if (omitted > 0) parts.push(`and ${omitted} more`);

	return {
		clause: parts.join(multi ? '; ' : ', '),
		constraints
	};
}

function withConstraints(sentence: string, clause: string): string {
	if (!clause) return sentence;
	return `${sentence.slice(0, -1)} (${clause}).`;
}

/**
 * A grant, ACL, or SSH rule whose source and target are the same selector.
 * Relation edges are not self-access, even when they point at the same node.
 */
export function isSelfAccessEdge(edge: Pick<GraphEdge, 'source' | 'target' | 'type'>): boolean {
	return edge.source === edge.target && SELF_ACCESS_TYPES.has(edge.type as AccessEdgeKind);
}

/**
 * Summarize self-access edges for one node. Non-self edges are ignored.
 * Returns null when none of the edges are self-access.
 */
export function summarizeSelfAccess(nodeType: string, edges: GraphEdge[]): SelfAccessSummary | null {
	const selfEdges = edges.filter(isSelfAccessEdge);
	if (selfEdges.length === 0) return null;

	const buckets: KindBucket[] = [];
	const ssh: SshUsers = { open: false, users: [] };
	const bucketFor = (kind: AccessEdgeKind): KindBucket => {
		const existing = buckets.find((bucket) => bucket.kind === kind);
		if (existing) return existing;
		const created: KindBucket = { kind, specs: [] };
		buckets.push(created);
		return created;
	};

	for (const edge of selfEdges) {
		const meta = edgeMeta(edge);
		if (edge.type === 'grant') addGrant(bucketFor('grant'), meta);
		else if (edge.type === 'acl') addAcl(bucketFor('acl'), meta);
		else if (edge.type === 'ssh') {
			bucketFor('ssh');
			addSshUser(ssh, meta);
		}
	}

	const sshBucket = buckets.find((bucket) => bucket.kind === 'ssh');
	if (sshBucket) sshBucket.specs = sshSpecs(ssh);

	const { clause, constraints } = constraintClause(buckets);
	const tooltip = withConstraints(baseSentence(nodeType), clause);
	return {
		edgeIds: selfEdges.map((edge) => edge.id),
		constraints,
		tooltip,
		ariaLabel: tooltip
	};
}

/** Self-access summaries keyed by node id. One badge per node. */
export function collectSelfAccessByNode(
	nodes: Array<Pick<GraphNode, 'id' | 'type'>>,
	edges: GraphEdge[]
): Map<string, SelfAccessSummary> {
	const typeById = new Map(nodes.map((node) => [node.id, node.type]));
	const edgesByNode = new Map<string, GraphEdge[]>();

	for (const edge of edges) {
		if (!isSelfAccessEdge(edge)) continue;
		const list = edgesByNode.get(edge.source);
		if (list) list.push(edge);
		else edgesByNode.set(edge.source, [edge]);
	}

	const summaries = new Map<string, SelfAccessSummary>();
	for (const [nodeId, selfEdges] of edgesByNode) {
		const summary = summarizeSelfAccess(typeById.get(nodeId) ?? 'unknown', selfEdges);
		if (summary) summaries.set(nodeId, summary);
	}
	return summaries;
}
