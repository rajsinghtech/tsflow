import { writable, derived, get } from 'svelte/store';
import { policyGraph, tailnetUsers } from './policy-store';
import { filteredEdges, filteredNodes, devices } from './network-store';
import type { PolicyGraph, GraphEdge } from '$lib/policy-engine/types';
import type { NetworkLink, NetworkNode } from '$lib/types';
import {
	matchPolicyRulesForEdge as matchPolicyRulesForEdgePure,
	type PolicyRuleMatch
} from './policy-traffic-matcher';
export type { PolicyRuleMatch } from './policy-traffic-matcher';

// --- View Mode ---

export type ViewMode = 'traffic' | 'combined';
export const viewMode = writable<ViewMode>('traffic');

const RELATION_TYPES = new Set(['member-of', 'owns-tag', 'contains', 'resolves-to']);

// --- Policy context for nodes ---
// Builds a complete picture of each entity's policy associations

export interface PolicyRelations {
	// member-of: user/tag → group[]
	memberOf: Map<string, string[]>;
	// owns-tag: user/group → tag[]
	ownsTag: Map<string, string[]>;
	// tag owners (reverse): tag → owner[]
	tagOwnedBy: Map<string, string[]>;
}

export const policyRelations = derived(policyGraph, ($graph): PolicyRelations => {
	const memberOf = new Map<string, string[]>();
	const ownsTag = new Map<string, string[]>();
	const tagOwnedBy = new Map<string, string[]>();

	if (!$graph) return { memberOf, ownsTag, tagOwnedBy };

	for (const edge of $graph.edges) {
		if (edge.type === 'member-of') {
			// source=group, target=member → member belongs to group
			const groups = memberOf.get(edge.target) ?? [];
			groups.push(edge.source);
			memberOf.set(edge.target, groups);
		} else if (edge.type === 'owns-tag') {
			// source=owner, target=tag → owner controls tag
			const tags = ownsTag.get(edge.source) ?? [];
			tags.push(edge.target);
			ownsTag.set(edge.source, tags);
			// reverse: tag is owned by owner
			const owners = tagOwnedBy.get(edge.target) ?? [];
			owners.push(edge.source);
			tagOwnedBy.set(edge.target, owners);
		}
	}

	return { memberOf, ownsTag, tagOwnedBy };
});

// Autogroup role mappings — which user roles belong to which autogroups
const AUTOGROUP_ROLES: Record<string, Set<string>> = {
	'autogroup:admin': new Set(['admin', 'owner']),
	'autogroup:owner': new Set(['owner']),
	'autogroup:it-admin': new Set(['it-admin', 'admin', 'owner']),
	'autogroup:network-admin': new Set(['network-admin', 'admin', 'owner'])
};

// Build per-device policy context using policy definitions + device data + user roles
export const devicePolicyContext = derived(
	[policyRelations, devices, tailnetUsers],
	([$rels, $devices, $users]) => {
		// Build user loginName → role map from Users API
		const userRoles = new Map<string, string>();
		for (const u of $users) {
			if (u.loginName) userRoles.set(u.loginName, u.role);
		}

		// Build user → groups from policy
		const userGroups = new Map<string, Set<string>>();
		for (const [member, groups] of $rels.memberOf) {
			userGroups.set(member, new Set(groups));
		}

		const deviceContext = new Map<string, {
			groups: string[];
			tagOwners: string[];
			autogroups: string[];
		}>();

		for (const device of $devices) {
			const groups = new Set<string>();
			const tagOwners = new Set<string>();
			const autogroups = new Set<string>();

			if (device.user) {
				// User → groups from policy
				for (const g of userGroups.get(device.user) ?? []) {
					groups.add(g);
				}

				// All user-owned devices are autogroup:member
				autogroups.add('autogroup:member');

				// Role-based autogroups from Users API
				const role = userRoles.get(device.user);
				if (role) {
					for (const [ag, roles] of Object.entries(AUTOGROUP_ROLES)) {
						if (roles.has(role)) autogroups.add(ag);
					}
				}
			}

			// Tagged devices (no user) are in autogroup:tagged
			if (!device.user && device.tags?.length) {
				autogroups.add('autogroup:tagged');
			}

			// Device tags → who owns those tags
			for (const tag of device.tags ?? []) {
				for (const owner of $rels.tagOwnedBy.get(tag) ?? []) {
					tagOwners.add(owner);
				}
			}

			// Index by all addresses and hostname
			const ctx = {
				groups: [...groups],
				tagOwners: [...tagOwners],
				autogroups: [...autogroups]
			};

			for (const addr of device.addresses ?? []) {
				deviceContext.set(addr, ctx);
			}
			if (device.hostname) {
				deviceContext.set(device.hostname, ctx);
			}
		}

		return deviceContext;
	}
);

// Backward compat — expose the simple group map for NetworkNode badges
export const policyGroupMap = derived(policyRelations, ($rels) => $rels.memberOf);

// Match a traffic edge against policy rules
// Uses tag/user selectors from the traffic nodes rather than IP matching
export function matchPolicyRulesForEdge(
	edge: NetworkLink,
	srcTags: string[],
	srcUser: string | undefined,
	dstTags: string[],
	dstUser: string | undefined,
	srcIps?: string[],
	dstIps?: string[]
): PolicyRuleMatch[] {
	return matchPolicyRulesForEdgePure(
		edge,
		{
			graph: get(policyGraph),
			groupMap: get(policyGroupMap),
			deviceContext: get(devicePolicyContext)
		},
		srcTags,
		srcUser,
		dstTags,
		dstUser,
		srcIps,
		dstIps
	);
}

// --- Combined Mode: Policy overlay edges for the traffic graph ---

export interface PolicyOverlayEdge {
	id: string;
	source: string; // traffic node ID
	target: string; // traffic node ID
	edgeType: string; // grant, acl, ssh
	policySource: string; // policy selector
	policyTarget: string; // policy selector
	hasTraffic: boolean; // true if there's observed traffic on this path
}

// Build selector → traffic node ID mapping
// A traffic node matches a selector if it has a matching tag, user, group, or autogroup
function buildSelectorToNodeIds(
	nodes: NetworkNode[],
	groupMap: Map<string, string[]>,
	devContext: Map<string, { groups: string[]; tagOwners: string[]; autogroups: string[] }>
): Map<string, Set<string>> {
	const map = new Map<string, Set<string>>();

	function effectiveGroups(member: string): Set<string> {
		const groups = new Set<string>();
		const pending = [member];
		while (pending.length > 0) {
			const current = pending.pop()!;
			for (const group of groupMap.get(current) ?? []) {
				if (!groups.has(group)) {
					groups.add(group);
					pending.push(group);
				}
			}
		}
		return groups;
	}

	function addMapping(selector: string, nodeId: string) {
		const set = map.get(selector) ?? new Set();
		set.add(nodeId);
		map.set(selector, set);
	}

	for (const node of nodes) {
		// Tags
		for (const tag of node.tags ?? []) {
			addMapping(tag, node.id);
		}
		// User
		if (node.user) {
			addMapping(node.user, node.id);
		}
		// Wildcard matches all
		addMapping('*', node.id);
		// Groups (via user membership)
		if (node.user) {
			for (const group of effectiveGroups(node.user)) {
				addMapping(group, node.id);
			}
		}
		// Groups (via tag membership)
		for (const tag of node.tags ?? []) {
			for (const group of effectiveGroups(tag)) {
				addMapping(group, node.id);
			}
		}
		// Autogroups and enriched groups from device context
		for (const ip of node.ips ?? []) {
			const ctx = devContext.get(ip);
			if (!ctx) continue;
			for (const ag of ctx.autogroups) {
				addMapping(ag, node.id);
			}
			for (const g of ctx.groups) {
				addMapping(g, node.id);
				for (const group of effectiveGroups(g)) {
					addMapping(group, node.id);
				}
			}
		}
	}

	return map;
}

// Derive overlay edges: policy rules resolved to traffic node pairs
export const policyOverlayEdges = derived(
	[policyGraph, filteredNodes, filteredEdges, policyGroupMap, viewMode, devicePolicyContext],
	([$graph, $nodes, $edges, $groupMap, $mode, $devCtx]) => {
		if ($mode !== 'combined' || !$graph || $nodes.length === 0) return [];

		const selectorMap = buildSelectorToNodeIds($nodes, $groupMap, $devCtx);

		// Build a set of existing traffic edges for "hasTraffic" detection
		const trafficPairs = new Set<string>();
		for (const edge of $edges) {
			trafficPairs.add(`${edge.source}|${edge.target}`);
			trafficPairs.add(`${edge.target}|${edge.source}`);
		}

		const overlayEdges: PolicyOverlayEdge[] = [];
		const seen = new Set<string>();

		for (const pEdge of $graph.edges) {
			if (RELATION_TYPES.has(pEdge.type)) continue;

			const srcNodes = selectorMap.get(pEdge.source);
			const dstNodes = selectorMap.get(pEdge.target);
			if (!srcNodes || !dstNodes) continue;

			for (const srcId of srcNodes) {
				for (const dstId of dstNodes) {
					if (srcId === dstId) continue;
					const key = `${srcId}|${dstId}|${pEdge.type}`;
					if (seen.has(key)) continue;
					seen.add(key);

					const hasTraffic = trafficPairs.has(`${srcId}|${dstId}`);

					overlayEdges.push({
						id: `policy-overlay-${pEdge.id}-${srcId}-${dstId}`,
						source: srcId,
						target: dstId,
						edgeType: pEdge.type,
						policySource: pEdge.source,
						policyTarget: pEdge.target,
						hasTraffic
					});
				}
			}
		}

		return overlayEdges;
	}
);

// Derive traffic edges that have no matching policy rule (potential concern)
export const unmatchedTrafficEdges = derived(
	[policyGraph, filteredNodes, filteredEdges, policyGroupMap, viewMode],
	([$graph, $nodes, $edges, $groupMap, $mode]) => {
		if ($mode !== 'combined' || !$graph) return new Set<string>();

		const unmatched = new Set<string>();
		const nodeMap = new Map($nodes.map((n) => [n.id, n]));

		for (const edge of $edges) {
			const srcNode = nodeMap.get(edge.source);
			const dstNode = nodeMap.get(edge.target);
			if (!srcNode || !dstNode) continue;

			const matches = matchPolicyRulesForEdge(
				edge,
				srcNode.tags ?? [],
				srcNode.user,
				dstNode.tags ?? [],
				dstNode.user,
				srcNode.ips ?? [],
				dstNode.ips ?? []
			);

			if (matches.length === 0) {
				unmatched.add(edge.id);
			}
		}

		return unmatched;
	}
);
