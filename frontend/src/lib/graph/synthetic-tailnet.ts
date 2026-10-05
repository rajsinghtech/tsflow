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
