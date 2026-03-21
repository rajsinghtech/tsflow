import { parse as parseJsonc, printParseErrorCode, type ParseError } from "jsonc-parser";
import {
  type AccessEdgeMeta,
  type AclRule,
  type EdgeType,
  type GraphEdge,
  type GraphNode,
  type NodeType,
  type ParseResult,
  type PolicyWarning,
  type RuleRef,
  type SshRule,
  type TailnetPolicy
} from "./types";

const IPV4 = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/;

function isIp(value: string): boolean {
  return IPV4.test(value) || value.includes(":");
}

function isCidr(value: string): boolean {
  const [ip, mask] = value.split("/");
  if (!ip || !mask) {
    return false;
  }
  const n = Number(mask);
  return Number.isInteger(n) && n >= 0 && n <= 128 && isIp(ip);
}

function dedupePush(list: string[], value: string): void {
  if (!list.includes(value)) {
    list.push(value);
  }
}

interface NormalizeContext {
  hosts: Set<string>;
}

function normalizeNodeType(raw: string, ctx: NormalizeContext): NodeType {
  if (raw === "*") return "wildcard";
  if (raw.startsWith("group:")) return "group";
  if (raw.startsWith("tag:")) return "tag";
  if (raw.startsWith("autogroup:")) return "autogroup";
  if (raw.startsWith("ipset:")) return "ipset";
  if (raw.startsWith("svc:")) return "service";
  if (ctx.hosts.has(raw)) return "host";
  if (isCidr(raw)) return "cidr";
  if (isIp(raw)) return "ip";
  if (raw.includes("@") || raw.startsWith("user:") || raw.startsWith("@")) return "user";
  return "unknown";
}

function parseAclDst(raw: string): { selector: string; ports: string[] | undefined } {
  const lastColon = raw.lastIndexOf(":");
  if (lastColon <= 0 || raw.startsWith("svc:")) {
    return { selector: raw, ports: undefined };
  }
  const left = raw.slice(0, lastColon);
  const right = raw.slice(lastColon + 1);
  if (!right || !/^[\d,*-]+$/.test(right)) {
    return { selector: raw, ports: undefined };
  }
  return { selector: left, ports: right.split(",") };
}

class Builder {
  nodes = new Map<string, GraphNode>();
  edges: GraphEdge[] = [];
  warnings: PolicyWarning[] = [];
  nodeIdsBySelector: Record<string, string[]> = {};
  edgeSeq = 0;

  constructor(private readonly ctx: NormalizeContext) {}

  addWarning(warning: PolicyWarning): void {
    this.warnings.push(warning);
  }

  addNode(selector: string): string {
    const id = selector;
    if (!this.nodes.has(id)) {
      this.nodes.set(id, {
        id,
        type: normalizeNodeType(selector, this.ctx),
        label: selector,
        rawSelector: selector
      });
      this.nodeIdsBySelector[selector] = this.nodeIdsBySelector[selector] ?? [];
      dedupePush(this.nodeIdsBySelector[selector], id);
    }
    return id;
  }

  addEdge(type: EdgeType, source: string, target: string, meta?: AccessEdgeMeta | Record<string, unknown>): void {
    this.edges.push({
      id: `${type}:${this.edgeSeq++}`,
      type,
      source,
      target,
      meta
    });
  }

  build(): ParseResult["graph"] {
    return {
      nodes: Array.from(this.nodes.values()),
      edges: this.edges,
      nodeIdsBySelector: this.nodeIdsBySelector,
      warnings: this.warnings
    };
  }
}

function ensureArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
}

function addAccessEdges(
  builder: Builder,
  kind: "grants" | "acls" | "ssh",
  index: number,
  srcRaw: string[],
  dstRaw: string[],
  baseMeta: Omit<AccessEdgeMeta, "ruleRef">
): void {
  const ruleRef: RuleRef = { section: kind, index };
  const edgeTypeBySection: Record<"grants" | "acls" | "ssh", EdgeType> = {
    grants: "grant",
    acls: "acl",
    ssh: "ssh"
  };
  for (const src of srcRaw) {
    for (const dst of dstRaw) {
      const s = builder.addNode(src);
      const t = builder.addNode(dst);
      builder.addEdge(edgeTypeBySection[kind], s, t, {
        ...baseMeta,
        ruleRef
      });
    }
  }
}

function parseAcls(builder: Builder, policy: TailnetPolicy): void {
  const rules = policy.acls ?? [];
  rules.forEach((rule: AclRule, index) => {
    const src = ensureArray(rule.src);
    const dstSelectors: string[] = [];
    const portsAccumulator = new Set<string>();

    ensureArray(rule.dst).forEach((raw, dstIndex) => {
      const parsed = parseAclDst(raw);
      if (parsed.selector.startsWith("svc:")) {
        builder.addWarning({
          code: "UNSUPPORTED_SELECTOR_FOR_RULE",
          message: "svc: selectors are only supported in grant destinations",
          path: `acls[${index}].dst[${dstIndex}]`,
          ruleRef: { section: "acls", index }
        });
      }
      dstSelectors.push(parsed.selector);
      parsed.ports?.forEach((p) => portsAccumulator.add(p));
    });

    addAccessEdges(builder, "acls", index, src, dstSelectors, {
      action: rule.action,
      proto: rule.proto,
      ports: portsAccumulator.size > 0 ? Array.from(portsAccumulator) : undefined
    });
  });
}

function parseGrants(builder: Builder, policy: TailnetPolicy): void {
  const rules = policy.grants ?? [];
  rules.forEach((rule, index) => {
    addAccessEdges(builder, "grants", index, ensureArray(rule.src), ensureArray(rule.dst), {
      ip: ensureArray(rule.ip),
      app: rule.app,
      via: ensureArray(rule.via),
      srcPosture: ensureArray(rule.srcPosture)
    });
  });
}

function parseSsh(builder: Builder, policy: TailnetPolicy): void {
  const rules = policy.ssh ?? [];
  rules.forEach((rule: SshRule, index) => {
    ensureArray(rule.dst).forEach((d, dstIndex) => {
      if (d === "*") {
        builder.addWarning({
          code: "SSH_DST_WILDCARD",
          message: "SSH dst cannot be wildcard '*'.",
          path: `ssh[${index}].dst[${dstIndex}]`,
          ruleRef: { section: "ssh", index }
        });
      }
      if (d.startsWith("svc:")) {
        builder.addWarning({
          code: "UNSUPPORTED_SELECTOR_FOR_RULE",
          message: "svc: selectors are not valid SSH destinations",
          path: `ssh[${index}].dst[${dstIndex}]`,
          ruleRef: { section: "ssh", index }
        });
      }
    });

    addAccessEdges(builder, "ssh", index, ensureArray(rule.src), ensureArray(rule.dst), {
      action: rule.action,
      sshUsers: ensureArray(rule.users),
      checkPeriod: rule.checkPeriod,
      acceptEnv: ensureArray(rule.acceptEnv),
      srcPosture: ensureArray(rule.srcPosture)
    });
  });
}

function parseRelations(builder: Builder, policy: TailnetPolicy): void {
  for (const [groupName, members] of Object.entries(policy.groups ?? {})) {
    const groupId = builder.addNode(groupName);
    for (const member of ensureArray(members)) {
      const memberId = builder.addNode(member);
      builder.addEdge("member-of", groupId, memberId);
    }
  }

  for (const [tag, owners] of Object.entries(policy.tagOwners ?? {})) {
    const tagId = builder.addNode(tag);
    for (const owner of ensureArray(owners)) {
      const ownerId = builder.addNode(owner);
      builder.addEdge("owns-tag", ownerId, tagId);
    }
  }

  for (const [host, ipOrCidr] of Object.entries(policy.hosts ?? {})) {
    const hostId = builder.addNode(host);
    const targetId = builder.addNode(ipOrCidr);
    builder.addEdge("resolves-to", hostId, targetId);
  }

  for (const [ipset, members] of Object.entries(policy.ipsets ?? {})) {
    const ipsetId = builder.addNode(ipset);
    for (const member of ensureArray(members)) {
      const memberId = builder.addNode(member);
      builder.addEdge("contains", ipsetId, memberId);
    }
  }
}

export function parsePolicyText(policyText: string): ParseResult {
  const parseErrors: ParseError[] = [];
  const parsed = parseJsonc(policyText, parseErrors, {
    allowTrailingComma: true,
    disallowComments: false
  }) as TailnetPolicy | undefined;

  if (parseErrors.length > 0 || !parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    const errors = parseErrors.length
      ? parseErrors.map((err) => `${printParseErrorCode(err.error)} at offset ${err.offset}`)
      : ["Policy must be a JSON object."];
    return {
      graph: {
        nodes: [],
        edges: [],
        nodeIdsBySelector: {},
        warnings: []
      },
      errors
    };
  }

  const ctx: NormalizeContext = {
    hosts: new Set(Object.keys(parsed.hosts ?? {}))
  };
  const builder = new Builder(ctx);

  parseRelations(builder, parsed);
  parseGrants(builder, parsed);
  parseAcls(builder, parsed);
  parseSsh(builder, parsed);

  return {
    graph: builder.build(),
    errors: []
  };
}
