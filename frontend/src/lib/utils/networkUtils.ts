/**
 * Utility functions for network visualization components
 */

// IP address and port parsing utilities
export const extractIP = (address: string): string => {
	// IPv6 with brackets: [fe80::1]:8080
	if (address.startsWith('[') && address.includes(']:')) {
		return address.substring(1, address.indexOf(']:'));
	}
	// IPv4 or hostname with port: 192.168.1.1:8080
	const colonIndex = address.lastIndexOf(':');
	if (colonIndex > 0 && !address.includes('::')) {
		return address.substring(0, colonIndex);
	}
	// Plain IP or IPv6 without port
	return address;
};

export const extractPort = (address: string): number => {
	let port = 0;

	// IPv6 with brackets: [fe80::1]:8080
	if (address.startsWith('[') && address.includes(']:')) {
		const portStr = address.split(']:')[1];
		port = portStr ? parseInt(portStr, 10) : 0;
	}
	// IPv4 or hostname: 192.168.1.1:8080 or example.com:8080
	// Only extract port if there's exactly one colon (not IPv6)
	else {
		const colonCount = (address.match(/:/g) || []).length;
		if (colonCount === 1) {
			const portStr = address.split(':')[1];
			port = portStr ? parseInt(portStr, 10) : 0;
		}
	}

	// Validate port range (0-65535) per RFC standards
	if (isNaN(port) || port < 0 || port > 65535) {
		return 0;
	}

	return port;
};

// IP address classification utilities
export const isTailscaleIP = (ip: string): boolean => {
	return ip.startsWith('100.') || ip.startsWith('fd7a:115c:a1e0:');
};

export const isPrivateIP = (ip: string): boolean => {
	return (
		ip.startsWith('10.') ||
		ip.startsWith('192.168.') ||
		/^172\.(1[6-9]|2[0-9]|3[0-1])\./.test(ip) ||
		ip.startsWith('fc00:') ||
		ip.startsWith('fd')
	);
};

// Device lookup utilities
export const getDeviceName = (ip: string, devices: any[]): string => {
	const device = devices.find((d) => d.addresses?.some((addr: string) => addr === ip));
	if (device) {
		const shortName = device.name?.split('.')[0];
		return shortName || device.name || ip;
	}
	return ip;
};

import type {
	BaseNetworkNode,
	BaseNetworkLink,
	ProcessedNodeData,
	ProcessedEdgeData,
	TrafficStatistics,
} from './networkTypes';
import {
	NodeCategory,
	TrafficType,
	NetworkProtocol,
	WELL_KNOWN_PORTS,
} from './networkTypes';

// Byte formatting with better precision
export const formatBytes = (bytes: number): string => {
	if (!bytes || bytes === 0 || isNaN(bytes)) return '0 B';

	const k = 1024;
	const sizes = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
	const i = Math.floor(Math.log(bytes) / Math.log(k));

	if (i >= sizes.length) {
		return `${(bytes / Math.pow(k, sizes.length - 1)).toFixed(1)} ${sizes[sizes.length - 1]}`;
	}

	const value = bytes / Math.pow(k, i);
	const decimals = i === 0 ? 0 : value < 10 ? 1 : 0;

	return `${value.toFixed(decimals)} ${sizes[i]}`;
};

// Packet formatting for large numbers
export const formatPackets = (packets: number): string => {
	if (packets === 0) return '0';
	if (packets < 1000) return packets.toString();
	if (packets < 1000000) return `${(packets / 1000).toFixed(1)}K`;
	if (packets < 1000000000) return `${(packets / 1000000).toFixed(1)}M`;
	return `${(packets / 1000000000).toFixed(1)}B`;
};

// Get protocol for a port
export const getPortProtocol = (port: number, availableProtocols: string[] = []): NetworkProtocol => {
	if (WELL_KNOWN_PORTS[port]) {
		return WELL_KNOWN_PORTS[port].protocol;
	}

	if (availableProtocols.includes('UDP')) return NetworkProtocol.UDP;
	if (availableProtocols.includes('ICMP')) return NetworkProtocol.ICMP;

	return NetworkProtocol.TCP;
};

// Get service name for a port
export const getPortService = (port: number): string => {
	return WELL_KNOWN_PORTS[port]?.service || 'Unknown';
};

// Determine node category based on tags and characteristics
export const categorizeNode = (node: BaseNetworkNode): NodeCategory => {
	const tags = node.tags.map((tag) => tag.toLowerCase());

	if (tags.some((tag) => tag.includes('derp'))) return NodeCategory.DERP;
	if (tags.some((tag) => tag.includes('tailscale')) || node.isTailscale) return NodeCategory.TAILSCALE;
	if (tags.some((tag) => tag.includes('private'))) return NodeCategory.PRIVATE;
	if (tags.some((tag) => tag.includes('public'))) return NodeCategory.PUBLIC;
	if (tags.some((tag) => tag.includes('server')) || node.incomingPorts.size > 3)
		return NodeCategory.SERVER;
	if (node.outgoingPorts.size > node.incomingPorts.size) return NodeCategory.CLIENT;

	return NodeCategory.UNKNOWN;
};

// Process node data for rendering
export const processNodeData = (node: BaseNetworkNode): ProcessedNodeData => {
	const allIPs = node.ips || [node.ip];
	const ipv4Addresses = allIPs.filter((ip) => !ip.includes(':'));
	const ipv6Addresses = allIPs.filter((ip) => ip.includes(':'));

	const deviceTags = node.tags
		.filter((tag) => tag && tag.startsWith('tag:'))
		.map((tag) => tag.substring(4))
		.slice(0, 5);

	const allPorts = new Set([...node.incomingPorts, ...node.outgoingPorts]);
	const uniquePorts = Array.from(allPorts)
		.sort((a, b) => a - b)
		.slice(0, 20);

	const protocolsList = Array.from(node.protocols);
	const category = categorizeNode(node);

	return {
		ipv4Addresses,
		ipv6Addresses,
		deviceTags,
		uniquePorts,
		formattedBytes: formatBytes(node.totalBytes),
		protocolsList,
		category,
	};
};

// Process edge data for rendering
export const processEdgeData = (
	link: BaseNetworkLink,
	totalTraffic: number = 0
): ProcessedEdgeData => {
	const trafficRatio = totalTraffic > 0 ? link.totalBytes / totalTraffic : 0;
	const txPercentage = link.totalBytes > 0 ? (link.txBytes / link.totalBytes) * 100 : 0;
	const rxPercentage = link.totalBytes > 0 ? (link.rxBytes / link.totalBytes) * 100 : 0;

	return {
		formattedBytes: formatBytes(link.totalBytes),
		formattedPackets: formatPackets(link.packets),
		trafficRatio,
		txPercentage,
		rxPercentage,
	};
};

// Calculate traffic statistics
export const calculateTrafficStatistics = (
	nodes: BaseNetworkNode[],
	links: BaseNetworkLink[]
): TrafficStatistics => {
	const totalBytes = links.reduce((sum, link) => sum + link.totalBytes, 0);

	const virtualBytes = links
		.filter((link) => link.trafficType === 'virtual')
		.reduce((sum, link) => sum + link.totalBytes, 0);

	const subnetBytes = links
		.filter((link) => link.trafficType === 'subnet')
		.reduce((sum, link) => sum + link.totalBytes, 0);

	const exitBytes = links
		.filter((link) => link.trafficType === 'exit')
		.reduce((sum, link) => sum + link.totalBytes, 0);

	const physicalBytes = links
		.filter((link) => link.trafficType === 'physical')
		.reduce((sum, link) => sum + link.totalBytes, 0);

	return {
		totalBytes,
		virtualBytes,
		subnetBytes,
		exitBytes,
		physicalBytes,
		nodeCount: nodes.length,
		linkCount: links.length,
	};
};

// Filter nodes by tag
export const filterNodesByTag = (nodes: BaseNetworkNode[], tag: string): BaseNetworkNode[] =>
	nodes.filter((node) => node.tags.some((t) => t.toLowerCase().includes(tag.toLowerCase())));

// Filter nodes by traffic threshold
export const filterNodesByTraffic = (
	nodes: BaseNetworkNode[],
	threshold: number
): BaseNetworkNode[] => nodes.filter((node) => node.totalBytes >= threshold);

// Sort nodes by traffic
export const sortNodesByTraffic = (
	nodes: BaseNetworkNode[],
	descending = true
): BaseNetworkNode[] =>
	[...nodes].sort((a, b) => (descending ? b.totalBytes - a.totalBytes : a.totalBytes - b.totalBytes));

// Sort nodes by connections
export const sortNodesByConnections = (
	nodes: BaseNetworkNode[],
	descending = true
): BaseNetworkNode[] =>
	[...nodes].sort((a, b) =>
		descending ? b.connections - a.connections : a.connections - b.connections
	);
