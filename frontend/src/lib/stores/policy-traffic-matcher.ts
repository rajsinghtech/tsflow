import type { AccessEdgeMeta, PolicyGraph } from '$lib/policy-engine/types';
import type { NetworkLink } from '$lib/types';

const RELATION_TYPES = new Set(['member-of', 'owns-tag', 'contains', 'resolves-to']);

export interface PolicyRuleMatch {
	edgeType: string;
	source: string;
	target: string;
	meta?: AccessEdgeMeta;
	ruleIndex: number;
}

export interface DevicePolicyContextEntry {
	groups: string[];
	tagOwners: string[];
	autogroups: string[];
}

export interface PolicyTrafficMatchContext {
	graph: PolicyGraph | null;
	groupMap: Map<string, string[]>;
	deviceContext: Map<string, DevicePolicyContextEntry>;
}

interface ProtoPort {
	proto?: string;
	port?: number;
	portEnd?: number;
}

function parseGrantIpSpecs(ipSpecs: string[]): ProtoPort[] {
	return ipSpecs.map((spec) => {
		if (spec === '*') return {};
		const colonIdx = spec.indexOf(':');
		if (colonIdx === -1) return { proto: spec.toLowerCase() };
		const proto = spec.slice(0, colonIdx).trim().toLowerCase();
		const portStr = spec.slice(colonIdx + 1);
		if (!proto) return { proto: '__invalid__' };
		if (portStr === '*') return { proto };
		const range = portStr.split('-');
		if (range.length > 2 || !range.every((part) => /^\d+$/.test(part))) {
			return { proto: '__invalid__' };
		}
		const port = Number(range[0]);
		const portEnd = range.length === 2 ? Number(range[1]) : port;
		if (port < 1 || port > 65535 || portEnd < port || portEnd > 65535) {
			return { proto: '__invalid__' };
		}
		return range.length === 2 ? { proto, port, portEnd } : { proto, port };
	});
}

function matchesProtoPort(trafficProto: string, trafficPorts: Set<number>, allowed: ProtoPort[]): boolean {
	for (const spec of allowed) {
		if (!spec.proto && !spec.port) return true;
		if (spec.proto && spec.proto !== '*' && spec.proto !== trafficProto) continue;
		if (spec.port === undefined) return true;
		for (const trafficPort of trafficPorts) {
			if (spec.portEnd !== undefined) {
				if (trafficPort >= spec.port && trafficPort <= spec.portEnd) return true;
			} else if (trafficPort === spec.port) {
				return true;
			}
		}
	}
	return false;
}

function portsOverlap(rulePorts: string[], trafficPorts: Set<number>): boolean {
	for (const rulePort of rulePorts) {
		if (rulePort === '*') return true;
		const dashIdx = rulePort.indexOf('-');
		if (dashIdx !== -1) {
			const startText = rulePort.slice(0, dashIdx);
			const endText = rulePort.slice(dashIdx + 1);
			if (!/^\d+$/.test(startText) || !/^\d+$/.test(endText)) continue;
			const start = Number(startText);
			const end = Number(endText);
			if (start < 1 || end < start || end > 65535) continue;
			for (const trafficPort of trafficPorts) {
				if (trafficPort >= start && trafficPort <= end) return true;
			}
		} else {
			if (!/^\d+$/.test(rulePort)) continue;
			const port = Number(rulePort);
			if (port >= 1 && port <= 65535 && trafficPorts.has(port)) return true;
		}
	}
	return false;
}

/**
 * Match one graph link against policy access edges.
 *
 * This function is deliberately independent of Svelte stores so the direction
 * and deduplication rules can be tested with deterministic fixtures.
 */
export function matchPolicyRulesForEdge(
	edge: NetworkLink,
	context: PolicyTrafficMatchContext,
	srcTags: string[],
	srcUser: string | undefined,
	dstTags: string[],
	dstUser: string | undefined,
	srcIps: string[] = [],
	dstIps: string[] = []
): PolicyRuleMatch[] {
	const { graph, groupMap, deviceContext } = context;
	if (!graph) return [];

	function addEffectiveGroups(selectors: Set<string>) {
		const pending = [...selectors];
		while (pending.length > 0) {
			const member = pending.pop()!;
			for (const group of groupMap.get(member) ?? []) {
				if (!selectors.has(group)) {
					selectors.add(group);
					pending.push(group);
				}
			}
		}
	}

	function buildSelectors(tags: string[], user: string | undefined, ips: string[]) {
		const selectors = new Set<string>([...tags, ...(user ? [user] : []), '*']);
		for (const ip of ips) {
			const ctx = deviceContext.get(ip);
			if (!ctx) continue;
			for (const autogroup of ctx.autogroups) selectors.add(autogroup);
			for (const group of ctx.groups) selectors.add(group);
			for (const owner of ctx.tagOwners) selectors.add(owner);
		}
		addEffectiveGroups(selectors);
		return selectors;
	}

	const matches: PolicyRuleMatch[] = [];
	const matchedPolicyEdges = new Set<string>();
	const observedDirections = edge.directions?.length
		? edge.directions
		: [{ source: edge.source, target: edge.target, protocol: edge.protocol, ports: edge.ports }];

	for (const direction of observedDirections) {
		const forward = direction.source === edge.source && direction.target === edge.target;
		const directionSrcSelectors = buildSelectors(
			forward ? srcTags : dstTags,
			forward ? srcUser : dstUser,
			forward ? srcIps : dstIps
		);
		const directionDstSelectors = buildSelectors(
			forward ? dstTags : srcTags,
			forward ? dstUser : srcUser,
			forward ? dstIps : srcIps
		);

		for (const policyEdge of graph.edges) {
			if (RELATION_TYPES.has(policyEdge.type)) continue;
			if (!directionSrcSelectors.has(policyEdge.source) || !directionDstSelectors.has(policyEdge.target)) continue;
			if (matchedPolicyEdges.has(policyEdge.id)) continue;

			const meta = policyEdge.meta as AccessEdgeMeta | undefined;
			if (policyEdge.type === 'grant') {
				if (meta?.ip?.length && !matchesProtoPort(direction.protocol, direction.ports, parseGrantIpSpecs(meta.ip))) {
					continue;
				}
			} else if (policyEdge.type === 'acl') {
				if (meta?.proto && meta.proto !== '*' && direction.protocol && meta.proto !== direction.protocol) continue;
				if (meta?.ports?.length && !meta.ports.includes('*') && !portsOverlap(meta.ports, direction.ports)) continue;
			} else if (policyEdge.type === 'ssh') {
				if (direction.protocol !== 'tcp' || !direction.ports.has(22)) continue;
			}

			matchedPolicyEdges.add(policyEdge.id);
			matches.push({
				edgeType: policyEdge.type,
				source: policyEdge.source,
				target: policyEdge.target,
				meta,
				ruleIndex: meta?.ruleRef?.index ?? -1
			});
		}
	}

	return matches;
}
