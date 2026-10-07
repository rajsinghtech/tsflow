import { beforeEach, describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import type { AggregatedFlow } from '#lib/services/tailscale-service';
import type { Device } from '#lib/types';
import { convertAggregatedFlowsToNetworkLogs } from '#lib/utils/aggregate-logs';
// ui-store first, as the app's store index does: network-store and ui-store
// import each other and network-store only uses uiStore inside functions.
import './ui-store';
import { devices, filteredEdges, filteredNodes, networkLogs, networkStats, records, services } from './network-store';
import { filterStore } from './filter-store';

const start = new Date('2026-03-01T12:00:00.000Z');
const end = new Date('2026-03-01T14:00:00.000Z');

function device(id: string, hostname: string, address: string): Device {
	return {
		id,
		name: `${hostname}.example.ts.net`,
		hostname,
		addresses: [address],
		os: 'linux',
		clientVersion: '',
		lastSeen: '',
		online: true,
		user: '',
		authorized: true,
		isExternal: false
	};
}

function flow(src: string, dst: string, tx: number, rx: number, trafficType: string): AggregatedFlow {
	return {
		srcNodeId: src,
		dstNodeId: dst,
		trafficType,
		totalTxBytes: tx,
		totalRxBytes: rx,
		totalTxPkts: 1,
		totalRxPkts: 1,
		flowCount: 1,
		directional: true,
		protocol: 6
	};
}

// One device behind a subnet route plus the physical (transport) row that
// carries the same bytes, as the aggregated endpoint returns them.
const flows = [
	flow('10.20.0.5', 'nLaptop01CNTRL', 5000, 100, 'subnet'),
	flow('198.51.100.7', 'nLaptop01CNTRL', 0, 5200, 'physical'),
	flow('nLaptop01CNTRL', 'nServer01CNTRL', 300, 700, 'virtual')
];

describe('graph node totals', () => {
	beforeEach(() => {
		devices.set([device('nLaptop01CNTRL', 'laptop', '100.64.0.10'), device('nServer01CNTRL', 'server', '100.64.0.11')]);
		services.set({});
		records.set({});
		networkLogs.set(convertAggregatedFlowsToNetworkLogs(flows, start, end));
	});

	it('counts only the selected traffic types on a node card', () => {
		filterStore.setTrafficTypes(['virtual', 'subnet']);
		const laptop = get(filteredNodes).find((node) => node.id === 'nLaptop01CNTRL');
		expect(laptop).toBeDefined();
		expect(laptop!.totalBytes).toBe(6100);
		expect(laptop!.txBytes).toBe(400);
		expect(laptop!.rxBytes).toBe(5700);
		expect(laptop!.connections).toBe(2);
		// Every visible edge touches the laptop, so the header total and the
		// card agree.
		expect(get(networkStats).totalBytes).toBe(6100);
		expect(get(filteredEdges).some((edge) => edge.trafficType === 'physical')).toBe(false);
	});

	it('adds physical bytes only when physical is selected', () => {
		filterStore.setTrafficTypes(['virtual', 'subnet', 'physical']);
		const laptop = get(filteredNodes).find((node) => node.id === 'nLaptop01CNTRL');
		expect(laptop!.totalBytes).toBe(11300);
		expect(laptop!.connections).toBe(3);
	});
});
