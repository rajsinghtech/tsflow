// Run with: npx vite-node src/lib/utils/network-processor.bench.ts
import type { Device, NetworkLog } from '$lib/types';
import {
	indexedDeviceLookup,
	linearDeviceLookup,
	processNetworkLogsWithLookup
} from './network-processor';

function makeDevices(count: number): Device[] {
	const devices: Device[] = [];
	for (let i = 0; i < count; i++) {
		devices.push({
			id: `n${String(i).padStart(8, '0')}CNTRL`,
			name: `host-${i}.tailnet.ts.net`,
			hostname: `host-${i}`,
			user: `user${i % 50}@example.com`,
			addresses: [`100.${(i >> 16) & 255}.${(i >> 8) & 255}.${i & 255}`],
			os: 'linux',
			clientVersion: '',
			lastSeen: '',
			online: true,
			authorized: true,
			isExternal: false,
			tags: i % 7 === 0 ? ['tag:server'] : []
		});
	}
	return devices;
}

function makeLogs(devices: Device[]): NetworkLog[] {
	return devices.map((source, index) => {
		const destination = devices[(index + 1) % devices.length];
		return {
			logged: '2026-10-05T12:00:00Z',
			nodeId: source.id,
			start: '2026-10-05T12:00:00Z',
			end: '2026-10-05T12:00:00Z',
			virtualTraffic: [
				{
					proto: 6,
					src: source.id,
					dst: destination.id,
					txBytes: 1000,
					txPkts: 10,
					ports: [{ proto: 6, port: 443, bytes: 1000 }],
					directional: { protocolBytes: { '6': 1000 }, ports: [{ proto: 6, port: 443, bytes: 1000 }] }
				},
				{
					proto: 6,
					src: destination.id,
					dst: source.id,
					txBytes: 400,
					txPkts: 4,
					ports: [{ proto: 6, port: 443, bytes: 400 }],
					directional: { protocolBytes: { '6': 400 }, ports: [{ proto: 6, port: 443, bytes: 400 }] }
				}
			],
			subnetTraffic: [],
			physicalTraffic: []
		};
	});
}

function time(name: string, iterations: number, fn: () => void) {
	fn();
	const start = performance.now();
	for (let i = 0; i < iterations; i++) fn();
	const ms = (performance.now() - start) / iterations;
	console.log(`${name} ${ms.toFixed(1)} ms`);
}

for (const count of [2000, 5000, 20000]) {
	const devices = makeDevices(count);
	const logs = makeLogs(devices);
	const iterations = count >= 20000 ? 1 : 3;
	time(`linear ${count}`, iterations, () => {
		processNetworkLogsWithLookup(logs, linearDeviceLookup(devices), {}, {});
	});
	time(`indexed ${count}`, iterations, () => {
		processNetworkLogsWithLookup(logs, indexedDeviceLookup(devices), {}, {});
	});
}
