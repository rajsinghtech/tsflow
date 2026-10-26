import type { NetworkLog } from '#lib/types';
import { extractIP, extractPort } from './ip-utils';
import { getProtocolName, wellKnownPortName } from './protocol';

export interface PortUsageStat {
	port: number;
	name: string;
	protocol: string;
	txBytes: number;
	rxBytes: number;
	// Distinct peers seen on this port. Aggregated logs carry one row per
	// direction, so counting rows would show about two per pair.
	peers: number;
}

function portName(port: number): string {
	return wellKnownPortName(port) || (port > 0 ? `Port ${port}` : 'Unknown');
}

// Per-port usage for one node. `resolve` maps an address (IP:port or device
// id) to the IP the node's set uses.
export function portUsageForNode(
	logs: NetworkLog[],
	nodeIPs: Set<string>,
	resolve: (address: string) => string = extractIP
): PortUsageStat[] {
	const ports = new Map<string, PortUsageStat & { peerSet: Set<string> }>();

	for (const log of logs) {
		const traffic = [...(log.virtualTraffic || []), ...(log.exitTraffic || []), ...(log.subnetTraffic || [])];
		for (const t of traffic) {
			const srcIP = resolve(t.src);
			const dstIP = resolve(t.dst);
			const nodeIsSrc = nodeIPs.has(srcIP);
			const nodeIsDst = nodeIPs.has(dstIP);
			if (!nodeIsSrc && !nodeIsDst) continue;
			const peer = nodeIsSrc ? dstIP : srcIP;

			const add = (port: number, protocol: string, bytes: number) => {
				if (port <= 0) return;
				const key = `${port}-${protocol}`;
				const txAdd = nodeIsSrc ? bytes : 0;
				// A self-flow counts once as TX instead of also as RX.
				const rxAdd = nodeIsDst && !nodeIsSrc ? bytes : 0;
				let stat = ports.get(key);
				if (!stat) {
					stat = { port, name: portName(port), protocol, txBytes: 0, rxBytes: 0, peers: 0, peerSet: new Set() };
					ports.set(key, stat);
				}
				stat.txBytes += txAdd;
				stat.rxBytes += rxAdd;
				stat.peerSet.add(peer);
			};

			if (t.ports?.length) {
				for (const stat of t.ports) add(stat.port, getProtocolName(stat.proto), stat.bytes || 0);
			} else {
				const dstPort = extractPort(t.dst) || 0;
				if (dstPort !== 0) add(dstPort, getProtocolName(t.proto || 0), t.txBytes || 0);
			}
		}
	}

	return Array.from(ports.values())
		.map(({ peerSet, ...stat }) => ({ ...stat, peers: peerSet.size }))
		.sort((a, b) => b.txBytes + b.rxBytes - (a.txBytes + a.rxBytes));
}
