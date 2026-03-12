import type { Device, NetworkLog, NetworkNode, NetworkLink, TrafficType } from '$lib/types';
import { extractIP, extractPort, categorizeIP, ipMatches } from './ip-utils';
import { getProtocolName } from './protocol';

interface VIPServiceInfo {
	name: string;
	addrs: string[];
	tags?: string[];
}

interface StaticRecordInfo {
	addrs: string[];
	comment?: string;
}

interface ProcessedNetwork {
	nodes: NetworkNode[];
	links: NetworkLink[];
}

// Check if a string looks like an IP address (vs a device ID)
function isIPAddress(value: string): boolean {
	//Guard against null values
	if (!value) return false;
	// IPv4: contains dots and numbers
	if (/^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(:\d+)?$/.test(value)) return true;
	// IPv6: contains colons but starts with [ or has many colons
	if (value.startsWith('[') || (value.match(/:/g) || []).length > 1) return true;
	// Tailscale 100.x.x.x format
	if (value.startsWith('100.')) return true;
	return false;
}

// Get device name from IP or device ID
function getDeviceName(
	ipOrId: string,
	devices: Device[] = [],
	services: Record<string, VIPServiceInfo> = {},
	records: Record<string, StaticRecordInfo> = {}
): string {
	// If it's a device ID, look up directly
	if (!isIPAddress(ipOrId)) {
		const device = devices.find((d) => d.id === ipOrId);
		if (device) {
			const shortName = device.name.split('.')[0];
			return shortName || device.name;
		}
		return ipOrId; // Return ID as-is if no device found
	}

	// Extract IP from IP:port format
	const ip = extractIP(ipOrId);

	// Check regular devices by IP
	const device = devices.find((d) => d.addresses.some((addr) => ipMatches(ip, addr)));
	if (device) {
		const shortName = device.name.split('.')[0];
		return shortName || device.name;
	}

	// Check VIP services
	for (const [serviceName, serviceInfo] of Object.entries(services)) {
		if (serviceInfo.addrs?.some((addr) => ipMatches(ip, addr))) {
			const cleanName = serviceName.startsWith('svc:') ? serviceName.substring(4) : serviceName;
			return cleanName;
		}
	}

	// Check static records
	for (const [recordName, recordInfo] of Object.entries(records)) {
		if (recordInfo.addrs?.some((addr) => ipMatches(ip, addr))) {
			return recordName;
		}
	}

	return ip;
}

// Get device data from IP or device ID
function getDeviceData(ipOrId: string, devices: Device[] = []): Device | null {
	// If it's a device ID, look up directly
	if (!isIPAddress(ipOrId)) {
		return devices.find((d) => d.id === ipOrId) || null;
	}
	// Otherwise look up by IP
	const ip = extractIP(ipOrId);
	return devices.find((d) => d.addresses.some((addr) => ipMatches(ip, addr))) || null;
}

// Get service data from IP
function getServiceData(
	ipOrId: string,
	services: Record<string, VIPServiceInfo> = {}
): VIPServiceInfo | null {
	const ip = extractIP(ipOrId);
	for (const serviceInfo of Object.values(services)) {
		if (serviceInfo.addrs?.some((addr) => ipMatches(ip, addr))) {
			return serviceInfo;
		}
	}
	return null;
}

// Get the IP from either IP:port format or device ID
function resolveToIP(ipOrId: string, devices: Device[] = []): string {
	// If it's already an IP, extract it
	if (isIPAddress(ipOrId)) {
		return extractIP(ipOrId);
	}
	// If it's a device ID, get the first IP from the device
	const device = devices.find((d) => d.id === ipOrId);
	if (device && device.addresses.length > 0) {
		return device.addresses[0];
	}
	// Return the ID as-is (for external nodes)
	return ipOrId;
}

// Process network logs to create nodes and links
export function processNetworkLogs(
	logs: NetworkLog[],
	devices: Device[],
	services: Record<string, VIPServiceInfo> = {},
	records: Record<string, StaticRecordInfo> = {}
): ProcessedNetwork {
	if (!logs || logs.length === 0) {
		return { nodes: [], links: [] };
	}

	const nodeMap = new Map<string, NetworkNode>();
	const linkMap = new Map<string, NetworkLink>();

	logs.forEach((log) => {
		// Resolve the reporting node's identity for proper byte attribution
		const reporterIP = resolveToIP(log.nodeId, devices);

		const allTraffic = [
			...(log.virtualTraffic || []).map((t) => ({ ...t, type: 'virtual' as TrafficType })),
			...(log.exitTraffic || []).map((t) => ({ ...t, type: 'exit' as TrafficType })),
			...(log.subnetTraffic || []).map((t) => ({ ...t, type: 'subnet' as TrafficType })),
			...(log.physicalTraffic || []).map((t) => ({
				...t,
				type: 'physical' as TrafficType,
				proto: t.proto || 0
			}))
		];

		allTraffic.forEach((traffic) => {
			// Skip entries with missing src or dst (e.g. some exitTraffic entries
			// from the Tailscale API omit the src field)
			if (!traffic.src || !traffic.dst) return;

			// Handle both IP:port format (live) and device ID format (historical)
			const srcIP = resolveToIP(traffic.src, devices);
			const dstIP = resolveToIP(traffic.dst, devices);

			// Create or update source node
			const srcDeviceName = getDeviceName(traffic.src, devices, services, records);
			const srcNodeId = srcDeviceName !== srcIP ? srcDeviceName : srcIP;

			if (!nodeMap.has(srcNodeId)) {
				const isTailscale = categorizeIP(srcIP).includes('tailscale');
				const deviceData = getDeviceData(traffic.src, devices);
				const serviceData = getServiceData(traffic.src, services);
				const ipTags = categorizeIP(srcIP);
				const deviceTags = deviceData?.tags || [];
				const serviceTags = serviceData?.tags || [];
				const allTags = [...new Set([...ipTags, ...deviceTags, ...serviceTags])];

				nodeMap.set(srcNodeId, {
					id: srcNodeId,
					ip: srcIP,
					displayName: srcDeviceName,
					nodeType: 'ip',
					totalBytes: 0,
					txBytes: 0,
					rxBytes: 0,
					connections: 0,
					tags: allTags,
					user: deviceData?.user,
					isTailscale,
					ips: [srcIP],
					incomingPorts: new Set<number>(),
					outgoingPorts: new Set<number>(),
					protocols: new Set<string>(),
					device: deviceData || undefined,
					isVIPService: serviceData !== null
				});
			} else {
				const existingNode = nodeMap.get(srcNodeId)!;
				if (!existingNode.ips.includes(srcIP)) {
					existingNode.ips.push(srcIP);
					const newTags = categorizeIP(srcIP);
					const deviceData = getDeviceData(traffic.src, devices);
					const serviceData = getServiceData(traffic.src, services);
					const deviceTags = deviceData?.tags || [];
					const serviceTags = serviceData?.tags || [];
					[...newTags, ...deviceTags, ...serviceTags].forEach((tag) => {
						if (!existingNode.tags.includes(tag)) {
							existingNode.tags.push(tag);
						}
					});
					if (!existingNode.user && deviceData?.user) {
						existingNode.user = deviceData.user;
					}
				}
			}

			// Create or update destination node
			const dstDeviceName = getDeviceName(traffic.dst, devices, services, records);
			const dstNodeId = dstDeviceName !== dstIP ? dstDeviceName : dstIP;

			if (!nodeMap.has(dstNodeId)) {
				const isTailscale = categorizeIP(dstIP).includes('tailscale');
				const deviceData = getDeviceData(traffic.dst, devices);
				const serviceData = getServiceData(traffic.dst, services);
				const ipTags = categorizeIP(dstIP);
				const deviceTags = deviceData?.tags || [];
				const serviceTags = serviceData?.tags || [];
				const allTags = [...new Set([...ipTags, ...deviceTags, ...serviceTags])];

				nodeMap.set(dstNodeId, {
					id: dstNodeId,
					ip: dstIP,
					displayName: dstDeviceName,
					nodeType: 'ip',
					totalBytes: 0,
					txBytes: 0,
					rxBytes: 0,
					connections: 0,
					tags: allTags,
					user: deviceData?.user,
					isTailscale,
					ips: [dstIP],
					incomingPorts: new Set<number>(),
					outgoingPorts: new Set<number>(),
					protocols: new Set<string>(),
					device: deviceData || undefined,
					isVIPService: serviceData !== null
				});
			} else {
				const existingNode = nodeMap.get(dstNodeId)!;
				if (!existingNode.ips.includes(dstIP)) {
					existingNode.ips.push(dstIP);
					const newTags = categorizeIP(dstIP);
					const deviceData = getDeviceData(traffic.dst, devices);
					const serviceData = getServiceData(traffic.dst, services);
					const deviceTags = deviceData?.tags || [];
					const serviceTags = serviceData?.tags || [];
					[...newTags, ...deviceTags, ...serviceTags].forEach((tag) => {
						if (!existingNode.tags.includes(tag)) {
							existingNode.tags.push(tag);
						}
					});
					if (!existingNode.user && deviceData?.user) {
						existingNode.user = deviceData.user;
					}
				}
			}

			// Update node traffic volumes using TX-only approach.
			// Only use txBytes to avoid double-counting when both endpoints report the
			// same connection. Each byte is counted once as the sender's TX.
			// rxBytes mirrors the peer's txBytes and would duplicate if both report.
			const srcNode = nodeMap.get(srcNodeId)!;
			const dstNode = nodeMap.get(dstNodeId)!;

			// txBytes always represents what the src sent to dst
			srcNode.txBytes += traffic.txBytes || 0;
			dstNode.rxBytes += traffic.txBytes || 0;
			srcNode.totalBytes = srcNode.txBytes + srcNode.rxBytes;
			dstNode.totalBytes = dstNode.txBytes + dstNode.rxBytes;

			// Track port and protocol information
			const protocolName = getProtocolName(traffic.proto || 0);
			srcNode.protocols.add(protocolName);
			dstNode.protocols.add(protocolName);

			// Only track ports for virtual and subnet traffic (not physical)
			// Physical traffic represents underlying transport (DERP, direct) with different port semantics
			if (traffic.type !== 'physical' && (traffic.proto === 6 || traffic.proto === 17)) {
				const srcPort = extractPort(traffic.src);
				const dstPort = extractPort(traffic.dst);

				// Track ports with correct direction semantics:
				// - Source node: outgoing port (ephemeral, less meaningful)
				// - Destination node: incoming port (service port, meaningful for analysis)
				if (srcPort !== null) {
					srcNode.outgoingPorts.add(srcPort);
				}
				if (dstPort !== null) {
					dstNode.incomingPorts.add(dstPort);
				}
			}

			// Create or update link - use bidirectional key to combine A->B and B->A
			const sortedIds = [srcNodeId, dstNodeId].sort();
			const linkKey = `${sortedIds[0]}<->${sortedIds[1]}|${traffic.type}`;

			if (!linkMap.has(linkKey)) {
				linkMap.set(linkKey, {
					id: linkKey,
					source: sortedIds[0],
					target: sortedIds[1],
					originalSource: srcIP,
					originalTarget: dstIP,
					totalBytes: 0,
					txBytes: 0,
					rxBytes: 0,
					packets: 0,
					protocol: protocolName,
					trafficType: traffic.type,
					ports: new Set<number>()
				});
			}

			// TX-only link accounting: attribute txBytes based on direction relative
			// to the sorted link key. This avoids double-counting when both endpoints report.
			const link = linkMap.get(linkKey)!;
			const isSrcFirst = srcNodeId === sortedIds[0];
			if (isSrcFirst) {
				link.txBytes += traffic.txBytes || 0;
			} else {
				link.rxBytes += traffic.txBytes || 0;
			}
			link.totalBytes = link.txBytes + link.rxBytes;
			link.packets += traffic.txPkts || 0;
		});
	});

	// Update connection counts
	linkMap.forEach((link) => {
		const srcNode = nodeMap.get(link.source);
		const dstNode = nodeMap.get(link.target);
		if (srcNode) srcNode.connections++;
		if (dstNode) dstNode.connections++;
	});

	return {
		nodes: Array.from(nodeMap.values()),
		links: Array.from(linkMap.values())
	};
}
