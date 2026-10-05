import { describe, expect, it } from 'vitest';
import type { Device, NetworkLog, NetworkNode, NetworkLink } from '#lib/types';
import {
	indexedDeviceLookup,
	linearDeviceLookup,
	processNetworkLogsWithLookup
} from './network-processor';

function device(overrides: Partial<Device> & Pick<Device, 'id' | 'name'>): Device {
	return {
		hostname: overrides.hostname ?? overrides.name,
		addresses: overrides.addresses ?? [],
		os: 'linux',
		clientVersion: '',
		lastSeen: '',
		online: true,
		user: overrides.user ?? '',
		authorized: true,
		isExternal: false,
		tags: overrides.tags,
		...overrides
	};
}

function log(entries: NetworkLog['virtualTraffic'], extra: Partial<NetworkLog> = {}): NetworkLog {
	return {
		logged: '2026-10-05T12:00:00Z',
		nodeId: 'log',
		start: '2026-10-05T12:00:00Z',
		end: '2026-10-05T12:00:00Z',
		virtualTraffic: entries,
		subnetTraffic: extra.subnetTraffic ?? [],
		physicalTraffic: extra.physicalTraffic ?? [],
		exitTraffic: extra.exitTraffic,
		...extra
	};
}

function entry(
	src: string,
	dst: string,
	bytes = 100,
	proto = 6
): NetworkLog['virtualTraffic'][number] {
	return {
		proto,
		src,
		dst,
		txBytes: bytes,
		txPkts: 1,
		ports: [{ proto, port: 443, bytes }],
		directional: { protocolBytes: { [String(proto)]: bytes }, ports: [{ proto, port: 443, bytes }] }
	};
}

function snapshot(nodes: NetworkNode[], links: NetworkLink[]) {
	return {
		nodes: nodes.map((node) => ({
			id: node.id,
			ip: node.ip,
			displayName: node.displayName,
			totalBytes: node.totalBytes,
			txBytes: node.txBytes,
			rxBytes: node.rxBytes,
			connections: node.connections,
			tags: node.tags,
			user: node.user,
			isTailscale: node.isTailscale,
			ips: node.ips,
			isVIPService: node.isVIPService,
			deviceId: node.device?.id,
			incomingPorts: [...node.incomingPorts].sort((a, b) => a - b),
			outgoingPorts: [...node.outgoingPorts].sort((a, b) => a - b),
			protocols: [...node.protocols].sort()
		})),
		links: links.map((link) => ({
			id: link.id,
			source: link.source,
			target: link.target,
			originalSource: link.originalSource,
			originalTarget: link.originalTarget,
			totalBytes: link.totalBytes,
			txBytes: link.txBytes,
			rxBytes: link.rxBytes,
			packets: link.packets,
			protocol: link.protocol,
			trafficType: link.trafficType,
			ports: [...link.ports].sort((a, b) => a - b),
			directions: (link.directions ?? []).map((direction) => ({
				source: direction.source,
				target: direction.target,
				protocol: direction.protocol,
				ports: [...direction.ports].sort((a, b) => a - b)
			}))
		}))
	};
}

function expectSame(
	logs: NetworkLog[],
	devices: Device[],
	services: Record<string, { name: string; addrs: string[]; tags?: string[] }> = {},
	records: Record<string, { addrs: string[]; comment?: string }> = {}
) {
	const linear = processNetworkLogsWithLookup(logs, linearDeviceLookup(devices), services, records);
	const indexed = processNetworkLogsWithLookup(logs, indexedDeviceLookup(devices), services, records);
	expect(snapshot(indexed.nodes, indexed.links)).toEqual(snapshot(linear.nodes, linear.links));
	expect(indexed.nodes.map((node) => node.id)).toEqual(linear.nodes.map((node) => node.id));
	expect(indexed.links.map((link) => link.id)).toEqual(linear.links.map((link) => link.id));
	return indexed;
}

describe('indexed device lookup matches linear lookup', () => {
	it('keeps the first device when addresses are duplicated', () => {
		const devices = [
			device({ id: 'first', name: 'first.tailnet.ts.net', addresses: ['100.64.0.1', '10.0.0.1'], user: 'a@example.com', tags: ['tag:a'] }),
			device({ id: 'second', name: 'second.tailnet.ts.net', addresses: ['100.64.0.1', '10.0.0.2'], user: 'b@example.com', tags: ['tag:b'] })
		];
		const result = expectSame([
			log([
				entry('100.64.0.1:1234', '10.0.0.2:443', 80),
				entry('10.0.0.1:9', '192.0.2.10:80', 20)
			])
		], devices);
		expect(result.nodes.find((node) => node.id === 'first')?.displayName).toBe('first');
		expect(result.nodes.find((node) => node.id === 'second')?.displayName).toBe('second');
		expect(result.nodes.map((node) => node.id)).toEqual(['first', 'second', '192.0.2.10']);
	});

	it('follows device list order when the duplicate winner changes', () => {
		const shared = ['100.64.0.8'];
		const alpha = device({ id: 'alpha', name: 'alpha.tailnet.ts.net', addresses: shared });
		const beta = device({ id: 'beta', name: 'beta.tailnet.ts.net', addresses: shared });
		const logs = [log([entry('100.64.0.8:1', '192.0.2.1:443')])];
		const forward = expectSame(logs, [alpha, beta]);
		const reverse = expectSame(logs, [beta, alpha]);
		expect(forward.nodes[0].id).toBe('alpha');
		expect(reverse.nodes[0].id).toBe('beta');
	});

	it('resolves every address on a device and leaves unknown endpoints alone', () => {
		const devices = [
			device({
				id: 'multi',
				name: 'multi.tailnet.ts.net',
				addresses: ['100.64.0.5', '10.1.1.5', 'fd7a:115c:a1e0::5'],
				tags: ['tag:multi']
			})
		];
		const result = expectSame([
			log([
				entry('10.1.1.5:40000', '[fd7a:115c:a1e0:0000:0000:0000:0000:0005]:443', 40),
				entry('n00000099CNTRL', '198.51.100.9:22', 10),
				entry('203.0.113.4:1', 'multi', 5)
			])
		], devices);
		expect(result.nodes.map((node) => node.id)).toEqual([
			'multi',
			'n00000099CNTRL',
			'198.51.100.9',
			'203.0.113.4'
		]);
		expect(result.nodes[0].ips).toEqual([
			'10.1.1.5',
			'fd7a:115c:a1e0:0000:0000:0000:0000:0005',
			'100.64.0.5'
		]);
		expect(result.nodes[1].displayName).toBe('Unknown Tailscale node n000');
	});

	it('keeps the first id when ids are duplicated and still indexes a later unique address', () => {
		const devices = [
			device({ id: 'earlier', name: 'earlier.tailnet.ts.net', addresses: ['100.64.0.1'] }),
			device({ id: 'later', name: 'later.tailnet.ts.net', addresses: ['100.64.0.1', '100.64.0.2'] })
		];
		const result = expectSame([
			log([
				entry('earlier', '100.64.0.2:443', 30),
				entry('100.64.0.1:9', '192.0.2.8:80', 4)
			])
		], devices);
		expect(result.nodes.find((node) => node.id === 'earlier')?.displayName).toBe('earlier');
		expect(result.nodes.find((node) => node.id === 'later')?.displayName).toBe('later');
		expect(result.links.find((link) => link.id === 'earlier<->later|virtual')?.totalBytes).toBe(30);
	});

	it('matches services and static records the same way', () => {
		const result = expectSame(
			[log([entry('100.64.0.50:1', '192.0.2.50:443', 12)])],
			[],
			{ 'svc:web': { name: 'web', addrs: ['100.64.0.50'], tags: ['tag:svc'] } },
			{ printer: { addrs: ['192.0.2.50'] } }
		);
		expect(result.nodes.map((node) => node.id)).toEqual(['svc:web', 'record:printer']);
		expect(result.nodes[0].isVIPService).toBe(true);
	});

	it('preserves byte direction, self flows, and link order', () => {
		const devices = [
			device({ id: 'a', name: 'a.tailnet.ts.net', addresses: ['100.64.0.1'] }),
			device({ id: 'b', name: 'b.tailnet.ts.net', addresses: ['100.64.0.2'] })
		];
		const result = expectSame([
			log([
				entry('b', 'a', 40),
				entry('a', 'b', 100),
				entry('a', 'a', 7)
			], {
				subnetTraffic: [entry('a', '10.0.0.9:80', 3)],
				physicalTraffic: [{ ...entry('a', 'b', 1, 17), proto: 17 }]
			})
		], devices);
		expect(result.links.map((link) => link.id)).toEqual([
			'a<->b|virtual',
			'a<->a|virtual',
			'10.0.0.9<->a|subnet',
			'a<->b|physical'
		]);
		const virtual = result.links[0];
		expect(virtual.txBytes).toBe(100);
		expect(virtual.rxBytes).toBe(40);
		expect(result.nodes.find((node) => node.id === 'a')?.txBytes).toBe(111);
	});

	it('returns an empty graph for empty logs', () => {
		expectSame([], [device({ id: 'a', name: 'a', addresses: ['100.64.0.1'] })]);
	});
});
