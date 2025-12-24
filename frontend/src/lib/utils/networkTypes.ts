/**
 * Comprehensive type definitions for network visualization components
 */

// IP address range constants
export const TAILSCALE_IPV4_PREFIX = '100.';
export const TAILSCALE_IPV6_PREFIX = 'fd7a:115c:a1e0:';

export const PRIVATE_IPV4_RANGES = ['10.', '192.168.', '172.'] as const;
export const PRIVATE_IPV6_PREFIXES = ['fc00:', 'fd'] as const;

// Common UDP ports for protocol detection
export const COMMON_UDP_PORTS = [53, 67, 68, 123, 161, 162, 514, 1900] as const;

// Core network data structures
export interface BaseNetworkNode {
	id: string;
	ip: string;
	displayName: string;
	nodeType: 'ip';
	totalBytes: number;
	txBytes: number;
	rxBytes: number;
	connections: number;
	tags: string[];
	user?: string;
	isTailscale: boolean;
	ips?: string[];
	incomingPorts: Set<number>;
	outgoingPorts: Set<number>;
	protocols: Set<string>;
}

export interface BaseNetworkLink {
	source: string | BaseNetworkNode;
	target: string | BaseNetworkNode;
	originalSource: string;
	originalTarget: string;
	totalBytes: number;
	txBytes: number;
	rxBytes: number;
	packets: number;
	txPackets: number;
	rxPackets: number;
	protocol: string;
	trafficType: 'virtual' | 'subnet' | 'exit' | 'physical';
}

// Node categories
export enum NodeCategory {
	DERP = 'derp',
	TAILSCALE = 'tailscale',
	PRIVATE = 'private',
	PUBLIC = 'public',
	SERVER = 'server',
	CLIENT = 'client',
	UNKNOWN = 'unknown'
}

// Traffic type enumeration
export enum TrafficType {
	VIRTUAL = 'virtual',
	SUBNET = 'subnet',
	EXIT = 'exit',
	PHYSICAL = 'physical'
}

// Protocol types
export enum NetworkProtocol {
	TCP = 'TCP',
	UDP = 'UDP',
	ICMP = 'ICMP',
	HTTP = 'HTTP',
	HTTPS = 'HTTPS',
	SSH = 'SSH',
	DNS = 'DNS',
	DHCP = 'DHCP',
	NTP = 'NTP',
	SNMP = 'SNMP'
}

// Well-known ports mapping
export const WELL_KNOWN_PORTS: Record<number, { protocol: NetworkProtocol; service: string }> = {
	20: { protocol: NetworkProtocol.TCP, service: 'FTP Data' },
	21: { protocol: NetworkProtocol.TCP, service: 'FTP Control' },
	22: { protocol: NetworkProtocol.TCP, service: 'SSH' },
	23: { protocol: NetworkProtocol.TCP, service: 'Telnet' },
	25: { protocol: NetworkProtocol.TCP, service: 'SMTP' },
	53: { protocol: NetworkProtocol.UDP, service: 'DNS' },
	67: { protocol: NetworkProtocol.UDP, service: 'DHCP Server' },
	68: { protocol: NetworkProtocol.UDP, service: 'DHCP Client' },
	80: { protocol: NetworkProtocol.TCP, service: 'HTTP' },
	110: { protocol: NetworkProtocol.TCP, service: 'POP3' },
	123: { protocol: NetworkProtocol.UDP, service: 'NTP' },
	143: { protocol: NetworkProtocol.TCP, service: 'IMAP' },
	161: { protocol: NetworkProtocol.UDP, service: 'SNMP' },
	443: { protocol: NetworkProtocol.TCP, service: 'HTTPS' },
	993: { protocol: NetworkProtocol.TCP, service: 'IMAPS' },
	995: { protocol: NetworkProtocol.TCP, service: 'POP3S' },
	3306: { protocol: NetworkProtocol.TCP, service: 'MySQL' },
	3389: { protocol: NetworkProtocol.TCP, service: 'RDP' },
	5432: { protocol: NetworkProtocol.TCP, service: 'PostgreSQL' },
	6379: { protocol: NetworkProtocol.TCP, service: 'Redis' },
	27017: { protocol: NetworkProtocol.TCP, service: 'MongoDB' },
};

// Traffic statistics
export interface TrafficStatistics {
	totalBytes: number;
	virtualBytes: number;
	subnetBytes: number;
	exitBytes: number;
	physicalBytes: number;
	nodeCount: number;
	linkCount: number;
}

// Processed data types
export interface ProcessedNodeData {
	ipv4Addresses: string[];
	ipv6Addresses: string[];
	deviceTags: string[];
	uniquePorts: number[];
	formattedBytes: string;
	protocolsList: string[];
	category: NodeCategory;
}

export interface ProcessedEdgeData {
	formattedBytes: string;
	formattedPackets: string;
	trafficRatio: number;
	txPercentage: number;
	rxPercentage: number;
}
