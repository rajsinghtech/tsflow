import { type AccessEdgeKind, type GraphEdge, type PolicyGraph, type QueryFilters, type QueryResult } from "./types";

const ACCESS_TYPES: AccessEdgeKind[] = ["grant", "acl", "ssh"];
const RELATION_TYPES = new Set(["member-of", "owns-tag", "contains", "resolves-to"]);

function resolveFocusNodeId(graph: PolicyGraph, selectorOrNodeId: string): string | null {
  if (graph.nodes.some((n) => n.id === selectorOrNodeId)) {
    return selectorOrNodeId;
  }
  const matches = graph.nodeIdsBySelector[selectorOrNodeId] ?? [];
  return matches[0] ?? null;
}

function asAccessType(type: string): AccessEdgeKind | null {
  return ACCESS_TYPES.includes(type as AccessEdgeKind) ? (type as AccessEdgeKind) : null;
}

function filterAccessEdges(edges: GraphEdge[], filters?: QueryFilters): GraphEdge[] {
  const allowed = new Set(filters?.includeEdgeKinds ?? ACCESS_TYPES);
  return edges.filter((edge) => {
    const accessType = asAccessType(edge.type);
    return accessType !== null && allowed.has(accessType);
  });
}

// Resolve all selectors that effectively represent this node:
// the node itself + groups it belongs to + autogroups (via member-of edges)
function resolveEffectiveSelectors(graph: PolicyGraph, nodeId: string): Set<string> {
  const selectors = new Set<string>([nodeId]);
  for (const edge of graph.edges) {
    if (edge.type === "member-of" && edge.target === nodeId) {
      // member-of: source=group, target=member → this node is in source group
      selectors.add(edge.source);
    }
  }
  return selectors;
}

export function whatHasAccessTo(graph: PolicyGraph, selectorOrNodeId: string, filters?: QueryFilters): QueryResult {
  const focusNodeId = resolveFocusNodeId(graph, selectorOrNodeId);
  if (!focusNodeId) {
    return {
      focusNodeId: selectorOrNodeId,
      direction: "inbound",
      matches: [],
      touchedNodeIds: [],
      touchedEdgeIds: [],
      warnings: [
        {
          code: "UNKNOWN_REFERENCE",
          message: `Selector not found: ${selectorOrNodeId}`
        }
      ]
    };
  }

  // For inbound: find edges targeting the focus node OR any group it belongs to
  const effectiveSelectors = resolveEffectiveSelectors(graph, focusNodeId);

  const matches = filterAccessEdges(graph.edges, filters)
    .filter((edge) => effectiveSelectors.has(edge.target))
    .map((edge) => ({
      sourceNodeId: edge.source,
      targetNodeId: edge.target,
      edgeId: edge.id,
      edgeType: edge.type as AccessEdgeKind,
      ruleRef: (edge.meta as { ruleRef: { section: "grants" | "acls" | "ssh"; index: number } }).ruleRef
    }));

  return {
    focusNodeId,
    direction: "inbound",
    matches,
    touchedNodeIds: Array.from(new Set(matches.flatMap((m) => [m.sourceNodeId, m.targetNodeId]))),
    touchedEdgeIds: matches.map((m) => m.edgeId),
    warnings: graph.warnings
  };
}

export function whatCanAccess(graph: PolicyGraph, selectorOrNodeId: string, filters?: QueryFilters): QueryResult {
  const focusNodeId = resolveFocusNodeId(graph, selectorOrNodeId);
  if (!focusNodeId) {
    return {
      focusNodeId: selectorOrNodeId,
      direction: "outbound",
      matches: [],
      touchedNodeIds: [],
      touchedEdgeIds: [],
      warnings: [
        {
          code: "UNKNOWN_REFERENCE",
          message: `Selector not found: ${selectorOrNodeId}`
        }
      ]
    };
  }

  // For outbound: find edges from the focus node OR any group/autogroup it belongs to
  const effectiveSelectors = resolveEffectiveSelectors(graph, focusNodeId);

  const matches = filterAccessEdges(graph.edges, filters)
    .filter((edge) => effectiveSelectors.has(edge.source))
    .map((edge) => ({
      sourceNodeId: edge.source,
      targetNodeId: edge.target,
      edgeId: edge.id,
      edgeType: edge.type as AccessEdgeKind,
      ruleRef: (edge.meta as { ruleRef: { section: "grants" | "acls" | "ssh"; index: number } }).ruleRef
    }));

  return {
    focusNodeId,
    direction: "outbound",
    matches,
    touchedNodeIds: Array.from(new Set(matches.flatMap((m) => [m.sourceNodeId, m.targetNodeId]))),
    touchedEdgeIds: matches.map((m) => m.edgeId),
    warnings: graph.warnings
  };
}
