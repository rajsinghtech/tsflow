import type { AggregatedFlow } from '#lib/services/tailscale-service';
import type { NetworkLog, PortStat, TrafficEntry } from '#lib/types';

function normalizeProtocolBytes(protocolBytes: Record<string, number> | undefined, fallbackProtocol: number, fallbackBytes: number) {
	const normalized: Record<string, number> = {};
	for (const [rawProtocol, bytes] of Object.entries(protocolBytes || {})) {
		const protocol = Number(rawProtocol);
		if (!Number.isInteger(protocol) || protocol < 0 || !Number.isFinite(bytes)) continue;
		normalized[String(protocol)] = bytes;
	}
	if (Object.keys(normalized).length === 0 && fallbackBytes > 0) {
		normalized[String(fallbackProtocol || 0)] = fallbackBytes;
	}
	return normalized;
}

function dominantProtocol(protocolBytes: Record<string, number>, fallbackProtocol: number): number {
	let dominant = fallbackProtocol || 0;
	let dominantBytes = -1;
	for (const [rawProtocol, bytes] of Object.entries(protocolBytes)) {
		const protocol = Number(rawProtocol);
		if (!Number.isInteger(protocol) || !Number.isFinite(bytes)) continue;
		if (bytes > dominantBytes || (bytes === dominantBytes && protocol < dominant)) {
			dominant = protocol;
			dominantBytes = bytes;
		}
	}
	return dominant;
}

function mergePortStats(...lists: (PortStat[] | undefined)[]): PortStat[] {
	const merged = new Map<string, PortStat>();
	for (const list of lists) {
		for (const stat of list || []) {
			if (stat.port <= 0) continue;
			const key = `${stat.proto}:${stat.port}`;
			const existing = merged.get(key);
			if (existing) existing.bytes += stat.bytes || 0;
			else merged.set(key, { ...stat });
		}
	}
	return Array.from(merged.values());
}

function makeAggregateTrafficEntry(
	src: string,
	dst: string,
	txBytes: number,
	txPackets: number,
	rxBytes: number,
	rxPackets: number,
	protocolBytes: Record<string, number>,
	ports: PortStat[],
	directional: boolean
): TrafficEntry {
	const entry: TrafficEntry = {
		proto: dominantProtocol(protocolBytes, 0),
		src,
		dst,
		txBytes,
		rxBytes,
		txPkts: txPackets,
		rxPkts: rxPackets,
		ports
	};
	if (directional) {
		entry.directional = { protocolBytes, ports };
	}
	return entry;
}

// Convert pre-aggregated node-pair flows to NetworkLog format.
// Each direction is its own row and the graph totals use txBytes only.
// rxBytes is what that row's source received, so the log table can show RX.
export function convertAggregatedFlowsToNetworkLogs(flows: AggregatedFlow[], rangeStart: Date, rangeEnd: Date): NetworkLog[] {
	const logsByNode = new Map<string, NetworkLog>();
	const startISO = rangeStart.toISOString();
	const endISO = rangeEnd.toISOString();

	function getOrCreateLog(nodeId: string): NetworkLog {
		let log = logsByNode.get(nodeId);
		if (!log) {
			log = {
				logged: endISO,
				nodeId,
				start: startISO,
				end: endISO,
				virtualTraffic: [],
				exitTraffic: [],
				subnetTraffic: [],
				physicalTraffic: []
			};
			logsByNode.set(nodeId, log);
		}
		return log;
	}

	function pushTraffic(log: NetworkLog, trafficType: string, entry: TrafficEntry) {
		switch (trafficType) {
			case 'virtual':
				log.virtualTraffic.push(entry);
				break;
			case 'exit':
				log.exitTraffic!.push(entry);
				break;
			case 'subnet':
				log.subnetTraffic.push(entry);
				break;
			case 'physical':
				log.physicalTraffic.push(entry);
				break;
			default:
				log.virtualTraffic.push(entry);
				break;
		}
	}

	for (const flow of flows) {
		const directional = flow.directional === true;
		if (flow.srcNodeId === flow.dstNodeId) {
			// A self-pair is one graph endpoint. txBytes stays the combined
			// volume so the graph does not drop a direction. rxBytes stays the
			// received half for the log table.
			const totalBytes = (flow.totalTxBytes || 0) + (flow.totalRxBytes || 0);
			if (totalBytes > 0) {
				const selfLog = getOrCreateLog(flow.srcNodeId);
				const protocolBytes = directional
					? normalizeProtocolBytes(flow.txProtocolBytes, flow.protocol || 0, flow.totalTxBytes || 0)
					: normalizeProtocolBytes(undefined, flow.protocol || 0, totalBytes);
				if (directional) {
					const reverseProtocolBytes = normalizeProtocolBytes(flow.rxProtocolBytes, flow.protocol || 0, flow.totalRxBytes || 0);
					for (const [protocol, bytes] of Object.entries(reverseProtocolBytes)) {
						protocolBytes[protocol] = (protocolBytes[protocol] || 0) + bytes;
					}
				}
				const ports = directional
					? mergePortStats(flow.txPorts, flow.rxPorts)
					: flow.ports || [];
				pushTraffic(
					selfLog,
					flow.trafficType,
					makeAggregateTrafficEntry(
						flow.srcNodeId,
						flow.dstNodeId,
						totalBytes,
						(flow.totalTxPkts || 0) + (flow.totalRxPkts || 0),
						flow.totalRxBytes || 0,
						flow.totalRxPkts || 0,
						protocolBytes,
						ports,
						directional
					)
				);
			}
			continue;
		}

		// Forward direction: src sent txBytes to dst and received rxBytes back.
		if (flow.totalTxBytes > 0) {
			const fwdLog = getOrCreateLog(flow.srcNodeId);
			const protocolBytes = normalizeProtocolBytes(flow.txProtocolBytes, flow.protocol || 0, flow.totalTxBytes);
			const ports = directional ? flow.txPorts || [] : flow.ports || [];
			pushTraffic(
				fwdLog,
				flow.trafficType,
				makeAggregateTrafficEntry(
					flow.srcNodeId,
					flow.dstNodeId,
					flow.totalTxBytes,
					flow.totalTxPkts || 0,
					flow.totalRxBytes || 0,
					flow.totalRxPkts || 0,
					protocolBytes,
					ports,
					directional
				)
			);
		}

		// Reverse direction: dst sent the original rxBytes back to src.
		if (flow.totalRxBytes > 0) {
			const revLog = getOrCreateLog(flow.dstNodeId);
			const protocolBytes = normalizeProtocolBytes(flow.rxProtocolBytes, flow.protocol || 0, flow.totalRxBytes);
			const ports = directional ? flow.rxPorts || [] : [];
			pushTraffic(
				revLog,
				flow.trafficType,
				makeAggregateTrafficEntry(
					flow.dstNodeId,
					flow.srcNodeId,
					flow.totalRxBytes,
					flow.totalRxPkts || 0,
					flow.totalTxBytes || 0,
					flow.totalTxPkts || 0,
					protocolBytes,
					ports,
					directional
				)
			);
		}
	}

	return Array.from(logsByNode.values());
}
