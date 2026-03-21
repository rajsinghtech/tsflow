import { writable, derived, get } from 'svelte/store';
import { policyGraph, tailnetUsers } from './policy-store';
import { filteredEdges, filteredNodes, devices } from './network-store';
import type { PolicyGraph, GraphEdge, AccessEdgeMeta } from '$lib/policy-engine/types';
import type { NetworkLink, NetworkNode } from '$lib/types';

// --- View Mode ---

export type ViewMode = 'traffic' | 'combined';
export const viewMode = writable<ViewMode>('traffic');

const RELATION_TYPES = new Set(['member-of', 'owns-tag', 'contains', 'resolves-to']);

export interface PolicyRuleMatch {
	edgeType: string;
	source: string;
	target: string;
	meta?: AccessEdgeMeta;
	ruleIndex: number;
}

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

// --- Port/Protocol matching helpers ---

interface ProtoPort {
	proto?: string; // 'tcp', 'udp', or undefined for any
	port?: number; // specific port, or undefined for any
	portEnd?: number; // end of range if range specified
}

// Parse grant "ip" field specifiers like ["tcp:443", "udp:53", "*"]
function parseGrantIpSpecs(ipSpecs: string[]): ProtoPort[] {
	return ipSpecs.map((spec) => {
		if (spec === '*') return {};
		const colonIdx = spec.indexOf(':');
		if (colonIdx === -1) return { proto: spec };
		const proto = spec.slice(0, colonIdx).toLowerCase();
		const portStr = spec.slice(colonIdx + 1);
		if (portStr === '*') return { proto };
		const dashIdx = portStr.indexOf('-');
		if (dashIdx !== -1) {
			return { proto, port: parseInt(portStr.slice(0, dashIdx)), portEnd: parseInt(portStr.slice(dashIdx + 1)) };
		}
		return { proto, port: parseInt(portStr) };
	});
}

// Check if traffic proto:port matches any of the allowed specs
function matchesProtoPort(trafficProto: string, trafficPorts: Set<number>, allowed: ProtoPort[]): boolean {
	for (const spec of allowed) {
		if (!spec.proto && !spec.port) return true; // wildcard
		if (spec.proto && spec.proto !== trafficProto) continue;
		if (!spec.port) return true; // proto match, any port
		for (const tp of trafficPorts) {
			if (spec.portEnd) {
				if (tp >= spec.port && tp <= spec.portEnd) return true;
			} else if (tp === spec.port) {
				return true;
			}
		}
	}
	return false;
}

// Check if any ACL port spec overlaps with traffic ports
// ACL ports are strings like "22", "443", "1024-65535", "*"
function portsOverlap(rulePorts: string[], trafficPorts: Set<number>): boolean {
	for (const rp of rulePorts) {
		if (rp === '*') return true;
		const dashIdx = rp.indexOf('-');
		if (dashIdx !== -1) {
			const start = parseInt(rp.slice(0, dashIdx));
			const end = parseInt(rp.slice(dashIdx + 1));
			for (const tp of trafficPorts) {
				if (tp >= start && tp <= end) return true;
			}
		} else {
			if (trafficPorts.has(parseInt(rp))) return true;
		}
	}
	return false;
}

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
	const graph = get(policyGraph);
	if (!graph) return [];

	const srcSelectors = new Set<string>([
		...srcTags,
		...(srcUser ? [srcUser] : []),
		'*'
	]);
	const dstSelectors = new Set<string>([
		...dstTags,
		...(dstUser ? [dstUser] : []),
		'*'
	]);

	// Add groups that these selectors belong to
	const groupMap = get(policyGroupMap);
	for (const sel of [...srcSelectors]) {
		for (const group of groupMap.get(sel) ?? []) {
			srcSelectors.add(group);
		}
	}
	for (const sel of [...dstSelectors]) {
		for (const group of groupMap.get(sel) ?? []) {
			dstSelectors.add(group);
		}
	}

	// Add autogroups from device context
	const devCtx = get(devicePolicyContext);
	for (const ip of srcIps ?? []) {
		const ctx = devCtx.get(ip);
		if (ctx) {
			for (const ag of ctx.autogroups) srcSelectors.add(ag);
			for (const g of ctx.groups) srcSelectors.add(g);
		}
	}
	for (const ip of dstIps ?? []) {
		const ctx = devCtx.get(ip);
		if (ctx) {
			for (const ag of ctx.autogroups) dstSelectors.add(ag);
			for (const g of ctx.groups) dstSelectors.add(g);
		}
	}

	const matches: PolicyRuleMatch[] = [];
	const trafficProto = edge.protocol; // 'tcp', 'udp', etc
	const trafficPorts = edge.ports; // Set<number> of destination ports

	for (const pEdge of graph.edges) {
		if (RELATION_TYPES.has(pEdge.type)) continue;

		if (!srcSelectors.has(pEdge.source) || !dstSelectors.has(pEdge.target)) continue;

		const meta = pEdge.meta as AccessEdgeMeta | undefined;

		if (pEdge.type === 'grant') {
			// Grant rules use meta.ip for protocol:port specifiers
			// ip: ["*"] = all traffic allowed
			// ip: ["tcp:443", "tcp:8080"] = specific proto:port combos
			if (meta?.ip?.length) {
				const hasWildcard = meta.ip.includes('*');
				if (!hasWildcard && trafficPorts.size > 0) {
					const allowed = parseGrantIpSpecs(meta.ip);
					if (!matchesProtoPort(trafficProto, trafficPorts, allowed)) continue;
				}
			}
		} else if (pEdge.type === 'acl') {
			// ACL rules: meta.proto for protocol, meta.ports for destination ports
			// Check protocol match
			if (meta?.proto && trafficProto && meta.proto !== trafficProto) continue;
			// Check port match
			if (meta?.ports?.length && trafficPorts.size > 0) {
				if (!meta.ports.includes('*') && !portsOverlap(meta.ports, trafficPorts)) continue;
			}
		}
		// SSH rules: always match on port 22 implicitly, no port check needed

		matches.push({
			edgeType: pEdge.type,
			source: pEdge.source,
			target: pEdge.target,
			meta,
			ruleIndex: meta?.ruleRef?.index ?? -1
		});
	}

	return matches;
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
			for (const group of groupMap.get(node.user) ?? []) {
				addMapping(group, node.id);
			}
		}
		// Groups (via tag membership)
		for (const tag of node.tags ?? []) {
			for (const group of groupMap.get(tag) ?? []) {
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
