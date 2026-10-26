import { describe, expect, it } from 'vitest';
import type { AggregatedFlow } from '#lib/services/tailscale-service';
import { convertAggregatedFlowsToNetworkLogs } from './aggregate-logs';
import { portUsageForNode } from './port-usage';

function flow(src: string, dst: string, port: number, extra: Partial<AggregatedFlow> = {}): AggregatedFlow {
	return {
		srcNodeId: src,
		dstNodeId: dst,
		trafficType: 'virtual',
		totalTxBytes: 100,
		totalRxBytes: 50,
		totalTxPkts: 1,
		totalRxPkts: 1,
		flowCount: 1,
		protocol: 6,
		ports: [{ port, proto: 6, bytes: 100 }],
		directional: true,
		txPorts: [{ port, proto: 6, bytes: 100 }],
		rxPorts: [{ port, proto: 6, bytes: 50 }],
		...extra
	} as AggregatedFlow;
}

describe('portUsageForNode', () => {
	const start = new Date('2026-01-01T00:00:00Z');
	const end = new Date('2026-01-01T01:00:00Z');

	it('counts one peer for a pair that talks both ways on a port', () => {
		const logs = convertAggregatedFlowsToNetworkLogs([flow('a', 'b', 443)], start, end);
		const [stat] = portUsageForNode(logs, new Set(['a']));
		expect(stat.port).toBe(443);
		expect(stat.peers).toBe(1);
		expect(stat.txBytes).toBe(100);
		expect(stat.rxBytes).toBe(50);
	});

	it('counts each distinct peer once', () => {
		const logs = convertAggregatedFlowsToNetworkLogs(
			[flow('a', 'b', 443), flow('a', 'c', 443), flow('b', 'a', 443, { trafficType: 'subnet' })],
			start,
			end
		);
		const stat = portUsageForNode(logs, new Set(['a'])).find((s) => s.port === 443)!;
		expect(stat.peers).toBe(2);
	});
});
