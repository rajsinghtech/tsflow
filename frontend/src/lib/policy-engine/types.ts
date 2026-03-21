export interface TailnetPolicy {
  groups?: Record<string, string[]>;
  hosts?: Record<string, string>;
  tagOwners?: Record<string, string[]>;
  ipsets?: Record<string, string[]>;
  grants?: GrantRule[];
  acls?: AclRule[];
  ssh?: SshRule[];
}

export interface GrantRule {
  src: string[];
  dst: string[];
  ip?: string[];
  app?: string | Record<string, unknown>;
  srcPosture?: string[];
  via?: string[];
}

export interface AclRule {
  action: string;
  src: string[];
  dst: string[];
  proto?: string;
}

export interface SshRule {
  action: string;
  src: string[];
  dst: string[];
  users: string[];
  checkPeriod?: string;
  acceptEnv?: string[];
  srcPosture?: string[];
}

export type NodeType =
  | "user"
  | "group"
  | "tag"
  | "autogroup"
  | "host"
  | "ipset"
  | "service"
  | "ip"
  | "cidr"
  | "wildcard"
  | "unknown";

export type EdgeType =
  | "grant"
  | "acl"
  | "ssh"
  | "member-of"
  | "owns-tag"
  | "contains"
  | "resolves-to";

export interface GraphNode {
  id: string;
  type: NodeType;
  label: string;
  rawSelector?: string;
}

export interface RuleRef {
  section: "grants" | "acls" | "ssh";
  index: number;
}

export interface AccessEdgeMeta {
  ruleRef: RuleRef;
  action?: string;
  proto?: string;
  ports?: string[];
  ip?: string[];
  app?: string | Record<string, unknown>;
  via?: string[];
  srcPosture?: string[];
  sshUsers?: string[];
  checkPeriod?: string;
  acceptEnv?: string[];
}

export interface GraphEdge {
  id: string;
  type: EdgeType;
  source: string;
  target: string;
  meta?: AccessEdgeMeta | Record<string, unknown>;
}

export interface PolicyGraph {
  nodes: GraphNode[];
  edges: GraphEdge[];
  nodeIdsBySelector: Record<string, string[]>;
  warnings: PolicyWarning[];
}

export type WarningCode =
  | "UNSUPPORTED_RULE_SHAPE"
  | "UNSUPPORTED_SELECTOR_FOR_RULE"
  | "INVALID_SELECTOR"
  | "UNKNOWN_REFERENCE"
  | "SSH_DST_WILDCARD"
  | "SSH_RULE_CONSTRAINT"
  | "DUPLICATE_ENTITY";

export interface PolicyWarning {
  code: WarningCode;
  message: string;
  path?: string;
  ruleRef?: RuleRef;
}

export interface ParseResult {
  graph: PolicyGraph;
  errors: string[];
}

export type AccessEdgeKind = "grant" | "acl" | "ssh";

export interface QueryFilters {
  includeEdgeKinds?: AccessEdgeKind[];
}

export interface AccessMatch {
  sourceNodeId: string;
  targetNodeId: string;
  edgeId: string;
  edgeType: AccessEdgeKind;
  ruleRef: RuleRef;
}

export interface QueryResult {
  focusNodeId: string;
  direction: "inbound" | "outbound";
  matches: AccessMatch[];
  touchedNodeIds: string[];
  touchedEdgeIds: string[];
  warnings: PolicyWarning[];
}
