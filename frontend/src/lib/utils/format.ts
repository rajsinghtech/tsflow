export { formatBytes, formatPackets } from './networkUtils';

export function formatNumber(num: number): string {
	return num.toLocaleString();
}

export function formatDate(date: string | Date): string {
	const d = typeof date === 'string' ? new Date(date) : date;
	return d.toLocaleString();
}

export function getProtocolName(proto: string | number): string {
	// Handle string protocol names (from API)
	if (typeof proto === 'string') {
		const protoUpper = proto.toUpperCase();
		// Map common protocol names
		const stringMap: { [key: string]: string } = {
			'TCP': 'TCP',
			'UDP': 'UDP',
			'ICMP': 'ICMP',
			'IPV6-ICMP': 'ICMPv6',
			'ICMPV6': 'ICMPv6',
			'AH': 'AH',
			'ESP': 'ESP',
			'GRE': 'GRE',
			'SCTP': 'SCTP',
			'DCCP': 'DCCP',
			'IGMP': 'IGMP',
			'EGP': 'EGP',
			'IGP': 'IGP',
			'IPV4': 'IPv4',
			'IPV6': 'IPv6'
		};
		return stringMap[protoUpper] || protoUpper;
	}

	// Handle numeric protocol numbers (legacy/Go SDK)
	// Source: IANA Protocol Numbers Registry
	const protocols: { [key: number]: string } = {
		0: 'HOPOPT',
		1: 'ICMP',
		2: 'IGMP',
		6: 'TCP',
		17: 'UDP',
		33: 'DCCP',
		41: 'IPv6',
		47: 'GRE',
		50: 'ESP',
		51: 'AH',
		58: 'ICMPv6',
		89: 'OSPF',
		103: 'PIM',
		112: 'VRRP',
		132: 'SCTP',
		136: 'UDPLite',
		255: 'Reserved'
	};
	return protocols[proto] || `Proto-${proto}`;
}
