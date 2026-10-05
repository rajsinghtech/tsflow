import type { NetworkLink, NetworkNode } from '#lib/types';

export type SyntheticShape = 'tagged' | 'unique';

function device(id: string, ip: string, tag: string, user: string, index: number): NetworkNode {
	return {
		id,
		ip,
		displayName: `node-${index}`,
		nodeType: 'ip',
		totalBytes: 1000 + (index % 1000),
		txBytes: 1000 + (index % 1000),
		rxBytes: 0,
		connections: 1,
		tags: ['tailscale', tag],
		user,
		isTailscale: true,
		ips: [ip],
		incomingPorts: new Set<number>(),
		outgoingPorts: new Set<number>(),
		protocols: new Set<string>(['tcp']),
		isVIPService: false
	};
}

// One peer per node, the shape used to time the large-graph path.
export function syntheticTailnet(
	count: number,
	shape: SyntheticShape = 'tagged'
): { nodes: NetworkNode[]; edges: NetworkLink[] } {
	const nodes: NetworkNode[] = new Array(count);
	const edges: NetworkLink[] = [];

	for (let index = 0; index < count; index++) {
		const id = `n${index}`;
		let ip: string;
		let tag: string;
		let user: string;
		if (shape === 'unique') {
			ip = `10.${Math.floor(index / 256)}.${index % 256}.1`;
			tag = `tag:solo-${index}`;
			user = `user-${index}`;
		} else {
			ip = `100.64.${Math.floor(index / 256) % 256}.${index % 256}`;
			tag = `tag:pool-${index % 40}`;
			user = `owner-${index % 25}`;
		}
		nodes[index] = device(id, ip, tag, user, index);
		if (index === 0) continue;
		const left = `n${index - 1}`;
		const [source, target] = left < id ? [left, id] : [id, left];
		edges.push({
			id: `${source}<->${target}|virtual`,
			source,
			target,
			originalSource: source,
			originalTarget: target,
			totalBytes: 1500,
			txBytes: 1500,
			rxBytes: 0,
			packets: 1,
			protocol: 'tcp',
			trafficType: 'virtual',
			ports: new Set<number>([443])
		});
	}

	return { nodes, edges };
}

const ROLES = [
	'web', 'api', 'db', 'cache', 'queue', 'search', 'auth', 'billing', 'storage', 'batch',
	'ci', 'git', 'logs', 'ingress', 'nas', 'backup', 'mail', 'ml', 'build', 'camera',
	'iot', 'print', 'vpn', 'metrics'
];
const DEPTS = ['eng', 'sales', 'support', 'finance', 'ops', 'design', 'it', 'hr'];
const SITES = ['nyc', 'lon', 'fra', 'sjc', 'syd', 'sao'];
const K8S = [
	'kube-api', 'kube-dns', 'ingress', 'metrics-svc', 'auth-svc', 'billing-svc',
	'search-svc', 'queue-svc', 'cache-svc', 'db-proxy', 'mail-svc', 'ci-svc'
];

function blankNode(
	id: string,
	displayName: string,
	ip: string,
	tags: string[],
	user: string,
	bytes: number,
	ports: number[]
): NetworkNode {
	return {
		id,
		ip,
		displayName,
		nodeType: 'ip',
		totalBytes: bytes,
		txBytes: bytes,
		rxBytes: 0,
		connections: 1,
		tags,
		user,
		isTailscale: true,
		ips: [ip],
		incomingPorts: new Set(ports),
		outgoingPorts: new Set<number>(),
		protocols: new Set<string>(['tcp']),
		isVIPService: tags.includes('tag:k8s')
	};
}

function powerShares(parts: number): number[] {
	const raw = Array.from({ length: parts }, (_, index) => 1 / (index + 1) ** 1.2);
	const sum = raw.reduce((total, value) => total + value, 0);
	return raw.map((value) => value / sum);
}

function allocate(total: number, shares: number[], minimum: number): number[] {
	const counts = shares.map((share) => Math.max(minimum, Math.floor(total * share)));
	let drift = total - counts.reduce((sum, count) => sum + count, 0);
	let cursor = 0;
	let guard = 0;
	while (drift !== 0 && counts.length > 0 && guard < total + counts.length * 2) {
		guard += 1;
		const step = drift > 0 ? 1 : -1;
		if (counts[cursor] + step >= minimum) {
			counts[cursor] += step;
			drift -= step;
		}
		cursor = (cursor + 1) % counts.length;
	}
	return counts;
}

interface EdgeAcc {
	source: string;
	target: string;
	totalBytes: number;
	trafficType: NetworkLink['trafficType'];
}

// A tailnet with hubs, role tags, department laptops, and a few user-owned machines.
// Tag still wins over user, so the drawn graph is the role and hub groups plus the
// untagged user groups, not one device per row.
export function realisticTailnet(count: number): { nodes: NetworkNode[]; edges: NetworkLink[] } {
	if (count < 80) throw new Error('realisticTailnet needs at least 80 devices');
	const large = count >= 5000;
	const roles = ROLES.slice(0, large ? 10 : 8);
	const depts = DEPTS.slice(0, 4);
	const k8sNames = K8S.slice(0, large ? 10 : 8);
	const dnsCount = 3;
	const exitCount = large ? 4 : 2;
	const monCount = large ? 5 : 3;
	const fixed = dnsCount + exitCount + monCount + SITES.length + k8sNames.length;
	const remaining = count - fixed;
	const roleTotal = Math.round(remaining * 0.7);
	const laptopTotal = remaining - roleTotal;
	const personalTotal = Math.min(large ? 80 : 24, Math.max(8, Math.round(laptopTotal * 0.08)));
	const deptTotal = laptopTotal - personalTotal;
	const roleCounts = allocate(roleTotal, powerShares(roles.length), 2);
	const deptCounts = allocate(deptTotal, powerShares(depts.length), 2);
	const userCount = 4;
	const personalCounts = allocate(personalTotal, powerShares(userCount), 2);

	const nodes: NetworkNode[] = [];
	let serial = 0;
	const nextIp = () => {
		const octet = serial++;
		return `100.${64 + Math.floor(octet / 65536)}.${Math.floor(octet / 256) % 256}.${octet % 256}`;
	};
	const add = (
		id: string,
		name: string,
		tags: string[],
		user: string,
		bytes: number,
		ports: number[]
	) => {
		nodes.push(blankNode(id, name, nextIp(), ['tailscale', ...tags], user, bytes, ports));
		return id;
	};

	const dns: string[] = [];
	const exits: string[] = [];
	const mons: string[] = [];
	const routers: string[] = [];
	const k8s: string[] = [];
	const roleMembers: string[][] = roles.map(() => []);
	const deptMembers: string[][] = depts.map(() => []);
	const people: string[] = [];

	for (let i = 0; i < dnsCount; i++) dns.push(add(`dns-${i}`, `dns-${i + 1}`, ['tag:dns', 'tag:z-hub'], 'netops', 8_000_000_000, [53]));
	for (let i = 0; i < exitCount; i++) exits.push(add(`exit-${i}`, `exit-${i + 1}`, ['tag:exit', 'tag:z-hub'], 'netops', 3_000_000_000, [443]));
	for (let i = 0; i < monCount; i++) mons.push(add(`mon-${i}`, `mon-${i + 1}`, ['tag:monitoring', 'tag:z-hub'], 'netops', 900_000_000, [443]));
	for (let i = 0; i < SITES.length; i++) {
		const id = add(`router-${SITES[i]}`, `router-${SITES[i]}`, ['tag:subnet-router', `tag:z-router-${SITES[i]}`], `noc-${SITES[i]}`, 2_000_000_000, [443]);
		const router = nodes[nodes.length - 1];
		router.ip = `10.${i + 1}.0.1`;
		router.ips = [router.ip];
		routers.push(id);
	}
	for (let i = 0; i < k8sNames.length; i++) {
		k8s.push(add(k8sNames[i], k8sNames[i], ['tag:k8s', `tag:z-svc-${i}`], 'platform', 1_500_000_000 / (i + 1), [443]));
	}
	for (let r = 0; r < roles.length; r++) {
		for (let n = 0; n < roleCounts[r]; n++) {
			const person = `user-${(r * 17 + n) % (large ? 220 : 40)}`;
			people.push(person);
			roleMembers[r].push(
				add(
					`${roles[r]}-${n}`,
					`${roles[r]}-${n}`,
					[`tag:${roles[r]}`, `tag:z-site-${SITES[r % SITES.length]}`, `tag:z-env-${n % 5 === 0 ? 'prod' : 'dev'}`, `tag:z-user-${person}`],
					person,
					Math.round(50_000_000 / (r + 1)),
					[443]
				)
			);
		}
	}
	for (let d = 0; d < depts.length; d++) {
		for (let n = 0; n < deptCounts[d]; n++) {
			const person = `user-${(d * 13 + n) % (large ? 220 : 40)}`;
			people.push(person);
			deptMembers[d].push(
				add(
					`${depts[d]}-laptop-${n}`,
					`${depts[d]}-laptop-${n}`,
					[`tag:laptops-${depts[d]}`, `tag:z-site-${SITES[d % SITES.length]}`, `tag:z-user-${person}`],
					person,
					20_000_000,
					[]
				)
			);
		}
	}
	for (let u = 0; u < userCount; u++) {
		for (let n = 0; n < personalCounts[u]; n++) {
			add(`owner-${u}-${n}`, `owner-${u}-mbp-${n}`, [], `owner-${u}`, 5_000_000, []);
		}
	}

	while (nodes.length < count) {
		const n = nodes.length;
		add(`extra-${n}`, `extra-${n}`, ['tag:batch', 'tag:z-extra'], 'user-0', 1000, []);
	}
	if (nodes.length > count) nodes.length = count;

	const byId = new Map(nodes.map((node) => [node.id, node]));
	const edges = new Map<string, EdgeAcc>();
	const budget = count <= 1200 ? 2400 : count * 3;
	let rank = 0;
	const linkTo = (source: string, target: string, rankIndex: number, trafficType: NetworkLink['trafficType']) => {
		if (!source || !target || source === target) return;
		if (!byId.has(source) || !byId.has(target)) return;
		const [left, right] = source < target ? [source, target] : [target, source];
		const id = `${left}<->${right}|${trafficType}`;
		if (edges.size >= budget && !edges.has(id)) return;
		const bytes = Math.max(1000, Math.round(4_000_000_000 / (rankIndex + 1) ** 1.3));
		const existing = edges.get(id);
		if (existing) {
			existing.totalBytes += bytes;
			return;
		}
		edges.set(id, { source: left, target: right, totalBytes: bytes, trafficType });
	};

	const connectAll = (from: string[], to: string, trafficType: NetworkLink['trafficType']) => {
		for (const id of from) linkTo(id, to, rank++, trafficType);
	};

	for (let i = 0; i < k8s.length; i++) {
		linkTo(k8s[i], dns[i % dns.length], 2, 'virtual');
		const db = roleMembers[roles.indexOf('db')]?.[0];
		if (db) linkTo(k8s[i], db, 6, 'virtual');
		for (let j = i + 1; j < k8s.length; j++) linkTo(k8s[i], k8s[j], 3 + i + j, 'virtual');
	}
	for (let r = 0; r < roleMembers.length; r++) {
		connectAll(roleMembers[r], dns[r % dns.length], 'virtual');
		connectAll(roleMembers[r], routers[r % routers.length], 'subnet');
		if (r % 2 === 0) connectAll(roleMembers[r], mons[r % mons.length], 'virtual');
	}
	for (const members of deptMembers) connectAll(members, exits[rank % exits.length], 'virtual');
	for (const members of deptMembers) connectAll(members, dns[rank % dns.length], 'virtual');
	for (let i = 0; i < userCount; i++) {
		linkTo(`owner-${i}-0`, exits[i % exits.length], rank++, 'virtual');
		linkTo(`owner-${i}-0`, dns[i % dns.length], rank++, 'virtual');
	}

	const mesh = ['web', 'api', 'db', 'cache'].filter((role) => roles.includes(role));
	for (let i = 0; i < mesh.length; i++) {
		for (let j = i + 1; j < mesh.length; j++) {
			const a = roleMembers[roles.indexOf(mesh[i])][0];
			const b = roleMembers[roles.indexOf(mesh[j])][0];
			linkTo(a, b, i + j, 'virtual');
		}
	}
	for (let r = 0; r < roles.length; r++) {
		const a = roleMembers[r][0];
		const b = roleMembers[(r + 3) % roles.length][0];
		linkTo(a, b, 30 + r, 'virtual');
	}
	for (const node of nodes) {
		node.totalBytes = 0;
		node.txBytes = 0;
		node.connections = 0;
	}
	const links: NetworkLink[] = [];
	for (const [id, edge] of edges) {
		const source = byId.get(edge.source);
		const target = byId.get(edge.target);
		if (!source || !target) continue;
		source.connections += 1;
		target.connections += 1;
		source.totalBytes += edge.totalBytes;
		target.totalBytes += edge.totalBytes;
		source.txBytes = source.totalBytes;
		target.txBytes = target.totalBytes;
		links.push({
			id,
			source: edge.source,
			target: edge.target,
			originalSource: source.ip,
			originalTarget: target.ip,
			totalBytes: edge.totalBytes,
			txBytes: edge.totalBytes,
			rxBytes: 0,
			packets: 1,
			protocol: 'tcp',
			trafficType: edge.trafficType,
			ports: new Set<number>(target.incomingPorts)
		});
	}

	return { nodes, edges: links };
}
