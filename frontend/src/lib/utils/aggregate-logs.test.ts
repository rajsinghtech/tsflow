import { describe, expect, it } from 'vitest';
import type { AggregatedFlow } from '#lib/services/tailscale-service';
import { convertAggregatedFlowsToNetworkLogs } from '#lib/utils/aggregate-logs';
import { processNetworkLogs } from '#lib/utils/network-processor';

const start = new Date('2026-03-01T12:00:00.000Z');
const end = new Date('2026-03-01T13:00:00.000Z');

function flow(overrides: Partial<AggregatedFlow> & Pick<AggregatedFlow, 'srcNodeId' | 'dstNodeId'>): AggregatedFlow {
	return {
		trafficType: 'virtual',
		totalTxBytes: 0,
		totalRxBytes: 0,
		totalTxPkts: 1,
		totalRxPkts: 1,
		flowCount: 1,
		protocol: 6,
		...overrides
	};
}

function entries(flows: AggregatedFlow[]) {
	return convertAggregatedFlowsToNetworkLogs(flows, start, end).flatMap((log) => [
		...(log.virtualTraffic || []),
		...(log.exitTraffic || []),
		...(log.subnetTraffic || []),
		...(log.physicalTraffic || [])
	]);
}

describe('aggregated flow log bytes', () => {
	it('keeps received bytes on each directional log row', () => {
		const rows = entries([
			flow({
				srcNodeId: 'a',
				dstNodeId: 'b',
				totalTxBytes: 1000,
				totalRxBytes: 400,
				totalTxPkts: 3,
				totalRxPkts: 2
			})
		]);

		const forward = rows.find((row) => row.src === 'a' && row.dst === 'b');
		const reverse = rows.find((row) => row.src === 'b' && row.dst === 'a');
		expect(forward).toMatchObject({ txBytes: 1000, rxBytes: 400, txPkts: 3, rxPkts: 2 });
		expect(reverse).toMatchObject({ txBytes: 400, rxBytes: 1000, txPkts: 2, rxPkts: 3 });
		expect(rows.every((row) => row.rxBytes === 0)).toBe(false);
	});

	it('shows receive-only traffic as the sender transmitted bytes and zero received', () => {
		const rows = entries([
			flow({ srcNodeId: 'a', dstNodeId: 'b', totalTxBytes: 0, totalRxBytes: 250 })
		]);
		expect(rows).toHaveLength(1);
		expect(rows[0]).toMatchObject({ src: 'b', dst: 'a', txBytes: 250, rxBytes: 0 });
	});

	it('does not let log rx bytes change graph totals', () => {
		const logs = convertAggregatedFlowsToNetworkLogs(
			[
				flow({ srcNodeId: 'a', dstNodeId: 'b', totalTxBytes: 1000, totalRxBytes: 400 }),
				flow({
					srcNodeId: 'a',
					dstNodeId: 'a',
					trafficType: 'subnet',
					totalTxBytes: 50,
					totalRxBytes: 20
				})
			],
			start,
			end
		);
		const self = logs
			.flatMap((log) => log.subnetTraffic)
			.find((row) => row.src === 'a' && row.dst === 'a');
		expect(self).toMatchObject({ txBytes: 70, rxBytes: 20 });

		const graph = processNetworkLogs(logs, []);
		const virtual = graph.links.find((link) => link.id === 'a<->b|virtual');
		expect(virtual?.txBytes).toBe(1000);
		expect(virtual?.rxBytes).toBe(400);
		expect(virtual?.totalBytes).toBe(1400);
		const nodeA = graph.nodes.find((node) => node.id === 'a');
		expect(nodeA?.txBytes).toBe(1070);
		expect(nodeA?.rxBytes).toBe(400);
	});
});
