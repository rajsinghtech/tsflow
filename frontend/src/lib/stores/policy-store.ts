import { writable, derived, get } from 'svelte/store';
import type {
	PolicyGraph,
	GraphNode,
	GraphEdge,
	QueryResult,
	EdgeType,
	NodeType
} from '$lib/policy-engine/types';
import { parsePolicyText } from '$lib/policy-engine/parser';
import { whatCanAccess, whatHasAccessTo } from '$lib/policy-engine/query';

// --- Visibility types (local, not from library) ---

export interface NodeVisibilityOptions {
	showUserNodes: boolean;
	showGroupNodes: boolean;
	showTagNodes: boolean;
	showAutogroupNodes: boolean;
	showHostNodes: boolean;
	showIpsetNodes: boolean;
	showServiceNodes: boolean;
	showIpNodes: boolean;
	showCidrNodes: boolean;
	showWildcardNodes: boolean;
	showUnknownNodes: boolean;
}

export interface PolicyVisibilityOptions {
	showRelationEdges: boolean;
	showAclEdges: boolean;
	showGrantEdges: boolean;
	showSshEdges: boolean;
	hideIsolatedNodes: boolean;
	nodeVisibility: NodeVisibilityOptions;
}

const RELATION_EDGE_TYPES = new Set<EdgeType>([
	'member-of',
	'owns-tag',
	'contains',
	'resolves-to'
]);

const DEFAULT_VISIBILITY: PolicyVisibilityOptions = {
	showRelationEdges: true,
	showAclEdges: true,
	showGrantEdges: true,
	showSshEdges: true,
	hideIsolatedNodes: true,
	nodeVisibility: {
		showUserNodes: true,
		showGroupNodes: true,
		showTagNodes: true,
		showAutogroupNodes: true,
		showHostNodes: true,
		showIpsetNodes: true,
		showServiceNodes: true,
		showIpNodes: true,
		showCidrNodes: true,
		showWildcardNodes: true,
		showUnknownNodes: true
	}
};

const NODE_VISIBILITY_KEY: Record<NodeType, keyof NodeVisibilityOptions> = {
	user: 'showUserNodes',
	group: 'showGroupNodes',
	tag: 'showTagNodes',
	autogroup: 'showAutogroupNodes',
	host: 'showHostNodes',
	ipset: 'showIpsetNodes',
	service: 'showServiceNodes',
	ip: 'showIpNodes',
	cidr: 'showCidrNodes',
	wildcard: 'showWildcardNodes',
	unknown: 'showUnknownNodes'
};

// --- Writable stores ---

export const policyText = writable<string>('');
export const policyGraph = writable<PolicyGraph | null>(null);
export const parseErrors = writable<string[]>([]);
export const queryText = writable<string>('');
export const queryDirection = writable<'inbound' | 'outbound'>('outbound');
export const queryResult = writable<QueryResult | null>(null);
export const visibility = writable<PolicyVisibilityOptions>(DEFAULT_VISIBILITY);
export const isParsing = writable<boolean>(false);

export const fetchError = writable<string | null>(null);

// --- Tailnet Users (from API) ---

export interface TailnetUser {
	id: string;
	displayName: string;
	loginName: string;
	role: string;
	type: string;
	status: string;
	deviceCount: number;
	currentlyConnected: boolean;
}

export const tailnetUsers = writable<TailnetUser[]>([]);

// --- Actions ---

export async function fetchAndRenderPolicy(): Promise<void> {
	isParsing.set(true);
	fetchError.set(null);
	try {
		// Fetch policy and users in parallel
		const [policyResp, usersResp] = await Promise.all([
			fetch('/api/policy'),
			fetch('/api/users').catch(() => null) // Users are optional (needs users:read scope)
		]);

		if (!policyResp.ok) {
			throw new Error(`HTTP ${policyResp.status}: ${policyResp.statusText}`);
		}
		const text = await policyResp.text();
		policyText.set(text);
		const result = parsePolicyText(text);

		// Parse users if available and enrich the graph
		let users: TailnetUser[] = [];
		if (usersResp?.ok) {
			const usersData = await usersResp.json();
			users = usersData.users ?? [];
			tailnetUsers.set(users);
		}

		// Enrich: add API users who own devices but aren't in the ACL policy,
		// and create autogroup edges for all users
		if (users.length > 0) {
			const existingNodeIds = new Set(result.graph.nodes.map((n) => n.id));
			let edgeSeq = result.graph.edges.length;

			// Ensure autogroup nodes exist
			const autogroupNodes = ['autogroup:member', 'autogroup:admin', 'autogroup:owner', 'autogroup:tagged'];
			for (const ag of autogroupNodes) {
				if (!existingNodeIds.has(ag)) {
					result.graph.nodes.push({ id: ag, type: 'autogroup', label: ag, rawSelector: ag });
					result.graph.nodeIdsBySelector[ag] = [ag];
					existingNodeIds.add(ag);
				}
			}

			// Role → autogroup mapping
			const roleAutogroups: Record<string, string[]> = {
				owner: ['autogroup:member', 'autogroup:owner', 'autogroup:admin'],
				admin: ['autogroup:member', 'autogroup:admin'],
				'it-admin': ['autogroup:member'],
				'network-admin': ['autogroup:member'],
				'billing-admin': ['autogroup:member'],
				member: ['autogroup:member'],
				auditor: ['autogroup:member']
			};

			for (const u of users) {
				if (!u.loginName) continue;

				// Only inject users who own at least one device
				if (!existingNodeIds.has(u.loginName)) {
					if (!u.deviceCount) continue;
					result.graph.nodes.push({
						id: u.loginName,
						type: 'user',
						label: u.displayName || u.loginName,
						rawSelector: u.loginName
					});
					result.graph.nodeIdsBySelector[u.loginName] = [u.loginName];
					existingNodeIds.add(u.loginName);
				}

				// Create autogroup membership edges based on role
				const autogroups = roleAutogroups[u.role] ?? ['autogroup:member'];
				for (const ag of autogroups) {
					result.graph.edges.push({
						id: `enriched:${edgeSeq++}`,
						type: 'member-of',
						source: ag,
						target: u.loginName
					});
				}
			}
		}

		policyGraph.set(result.graph);
		parseErrors.set(result.errors);
		queryResult.set(null);
		queryText.set('');
	} catch (err: any) {
		fetchError.set(err.message ?? 'Failed to fetch policy');
	} finally {
		isParsing.set(false);
	}
}

export function renderPolicy(text: string): void {
	policyText.set(text);
	isParsing.set(true);
	try {
		const result = parsePolicyText(text);
		policyGraph.set(result.graph);
		parseErrors.set(result.errors);
		queryResult.set(null);
		queryText.set('');
	} finally {
		isParsing.set(false);
	}
}

export function runQuery(selector: string, direction: 'inbound' | 'outbound'): void {
	const graph = get(policyGraph);
	if (!graph || !selector.trim()) {
		queryResult.set(null);
		return;
	}
	queryText.set(selector);
	queryDirection.set(direction);
	const result =
		direction === 'outbound'
			? whatCanAccess(graph, selector.trim())
			: whatHasAccessTo(graph, selector.trim());
	queryResult.set(result);
}

export function clearQuery(): void {
	queryText.set('');
	queryResult.set(null);
}

export function toggleEdgeType(key: 'showRelationEdges' | 'showAclEdges' | 'showGrantEdges' | 'showSshEdges'): void {
	visibility.update((v) => ({ ...v, [key]: !v[key] }));
}

export function toggleNodeType(key: keyof NodeVisibilityOptions): void {
	visibility.update((v) => ({
		...v,
		nodeVisibility: { ...v.nodeVisibility, [key]: !v.nodeVisibility[key] }
	}));
}

export function toggleHideIsolated(): void {
	visibility.update((v) => ({ ...v, hideIsolatedNodes: !v.hideIsolatedNodes }));
}

export function resetPolicy(): void {
	policyText.set('');
	policyGraph.set(null);
	parseErrors.set([]);
	queryResult.set(null);
	queryText.set('');
	visibility.set(DEFAULT_VISIBILITY);
}

// --- Derived stores ---

function isEdgeVisible(edge: GraphEdge, vis: PolicyVisibilityOptions): boolean {
	if (RELATION_EDGE_TYPES.has(edge.type)) return vis.showRelationEdges;
	if (edge.type === 'acl') return vis.showAclEdges;
	if (edge.type === 'grant') return vis.showGrantEdges;
	if (edge.type === 'ssh') return vis.showSshEdges;
	return true;
}

function isNodeVisible(node: GraphNode, vis: PolicyVisibilityOptions): boolean {
	const key = NODE_VISIBILITY_KEY[node.type];
	return key ? vis.nodeVisibility[key] : true;
}

export const filteredGraph = derived(
	[policyGraph, visibility],
	([$graph, $vis]) => {
		if (!$graph) return { nodes: [] as GraphNode[], edges: [] as GraphEdge[] };

		const visibleNodes = $graph.nodes.filter((n) => isNodeVisible(n, $vis));
		const visibleNodeIds = new Set(visibleNodes.map((n) => n.id));
		const visibleEdges = $graph.edges.filter(
			(e) => isEdgeVisible(e, $vis) && visibleNodeIds.has(e.source) && visibleNodeIds.has(e.target)
		);

		if (!$vis.hideIsolatedNodes) {
			return { nodes: visibleNodes, edges: visibleEdges };
		}

		const connectedIds = new Set<string>();
		visibleEdges.forEach((e) => {
			connectedIds.add(e.source);
			connectedIds.add(e.target);
		});
		return {
			nodes: visibleNodes.filter((n) => connectedIds.has(n.id)),
			edges: visibleEdges
		};
	}
);

// Highlight query-matched nodes PLUS direct relation neighbors of the FOCUS node only
// (not transitive — only groups/tags the queried node directly belongs to)
export const highlightedPolicyNodeIds = derived(
	[queryResult, policyGraph],
	([$qr, $graph]) => {
		if (!$qr) return new Set<string>();
		const highlighted = new Set($qr.touchedNodeIds);

		// Only expand relations from the focus node, not from all touched nodes
		if ($graph && $qr.focusNodeId) {
			const focusId = $qr.focusNodeId;
			for (const edge of $graph.edges) {
				if (edge.type === 'member-of' || edge.type === 'owns-tag' || edge.type === 'contains' || edge.type === 'resolves-to') {
					if (edge.source === focusId || edge.target === focusId) {
						highlighted.add(edge.source);
						highlighted.add(edge.target);
					}
				}
			}
		}

		return highlighted;
	}
);

export const highlightedPolicyEdgeIds = derived(
	[queryResult, policyGraph, highlightedPolicyNodeIds],
	([$qr, $graph, $highlightedNodes]) => {
		if (!$qr) return new Set<string>();
		const highlighted = new Set($qr.touchedEdgeIds);

		// Highlight relation edges directly connected to the focus node
		if ($graph && $qr.focusNodeId) {
			const focusId = $qr.focusNodeId;
			for (const edge of $graph.edges) {
				if (edge.type === 'member-of' || edge.type === 'owns-tag' || edge.type === 'contains' || edge.type === 'resolves-to') {
					if (edge.source === focusId || edge.target === focusId) {
						highlighted.add(edge.id);
					}
				}
			}
		}

		return highlighted;
	}
);

export const hasQuery = derived(queryResult, ($qr) => $qr !== null && $qr.matches.length > 0);

export const parseSummary = derived(
	[policyGraph, parseErrors],
	([$graph, $errors]) => ({
		errorCount: $errors.length,
		warningCount: $graph?.warnings.length ?? 0,
		nodeCount: $graph?.nodes.length ?? 0,
		edgeCount: $graph?.edges.length ?? 0
	})
);

export const edgeTypeCounts = derived(filteredGraph, ($fg) => {
	const counts = { grant: 0, acl: 0, ssh: 0, relation: 0 };
	$fg.edges.forEach((e) => {
		if (RELATION_EDGE_TYPES.has(e.type)) counts.relation++;
		else if (e.type === 'grant') counts.grant++;
		else if (e.type === 'acl') counts.acl++;
		else if (e.type === 'ssh') counts.ssh++;
	});
	return counts;
});
