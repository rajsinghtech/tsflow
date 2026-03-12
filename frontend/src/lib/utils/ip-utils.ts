// Extract IP from address (remove port if present)
export function extractIP(address: string): string {
	//Guard Against Null Values
	if (!address) return '';
	// Handle IPv6 addresses like [fd7a:115c:a1e0::9001:b818]:62574
	if (address.startsWith('[') && address.includes(']:')) {
		return address.substring(1, address.indexOf(']:'));
	}

	// Handle IPv4 addresses with ports (format: 192.168.1.1:443)
	// IPv4 has exactly 3 dots, so we can safely split on last colon
	if (!address.includes('::') && (address.match(/\./g) || []).length === 3) {
		const colonIndex = address.lastIndexOf(':');
		if (colonIndex > 0) {
			return address.substring(0, colonIndex);
		}
	}

	// For IPv6 without brackets, return as-is (port should be in bracket format)
	return address;
}

// Extract port from address:port string
export function extractPort(address: string): number | null {
	//Guard Against Null Values
	if (!address) return null;
	// Handle IPv6 addresses like [fd7a:115c:a1e0::9001:b818]:62574
	if (address.startsWith('[') && address.includes(']:')) {
		const portStr = address.split(']:')[1];
		return portStr ? parseInt(portStr, 10) : null;
	}

	// IPv6 addresses without brackets have no port (port requires bracket notation)
	// Check for IPv6 by looking for multiple colons or :: compression
	if (address.includes('::') || (address.match(/:/g) || []).length > 1) {
		return null;
	}

	// Handle IPv4 addresses like 100.72.184.20:53221
	if (address.includes(':')) {
		const portStr = address.split(':').pop();
		const port = portStr ? parseInt(portStr, 10) : null;
		// Ensure we got a valid port number
		return port !== null && !isNaN(port) ? port : null;
	}

	return null;
}

// Categorize IP addresses by type
export function categorizeIP(ip: string): string[] {
	//Guard Against Null Values
	if (!ip) return ['null'];
	
	// DERP servers
	if (ip === '127.3.3.40') return ['derp'];

	// IPv4 Tailscale addresses (100.64.0.0/10 CGNAT range used by Tailscale)
	if (ip.startsWith('100.')) return ['tailscale'];

	// IPv6 Tailscale addresses (fd7a:115c:a1e0::/48 is Tailscale's IPv6 prefix)
	if (ip.startsWith('fd7a:115c:a1e0:')) return ['tailscale'];

	// IPv4 private addresses (RFC 1918)
	if (
		ip.startsWith('192.168.') ||
		ip.startsWith('10.') ||
		(ip.startsWith('172.') &&
			parseInt(ip.split('.')[1]) >= 16 &&
			parseInt(ip.split('.')[1]) <= 31)
	) {
		return ['private'];
	}

	// IPv6 private/link-local addresses (RFC 4193, RFC 4291)
	if (ip.startsWith('fe80:') || ip.startsWith('fc00:') || ip.startsWith('fd00:')) {
		if (!ip.startsWith('fd7a:115c:a1e0:')) {
			return ['private'];
		}
	}

	// IPv6 addresses (contains colons) - treat as public if not private or Tailscale
	if (ip.includes(':')) {
		return ['public'];
	}

	// Public IPv4 addresses
	return ['public'];
}

// Expand IPv6 address to full form for comparison
function expandIPv6(ip: string): string {
	// Remove brackets if present
	ip = ip.replace(/^\[|\]$/g, '').toLowerCase();

	// Split by ::
	const parts = ip.split('::');

	if (parts.length === 1) {
		// No :: compression, just ensure each group is 4 chars
		const groups = ip.split(':');
		return groups.map((g) => g.padStart(4, '0')).join(':');
	}

	// Handle :: compression
	const left = parts[0] ? parts[0].split(':') : [];
	const right = parts[1] ? parts[1].split(':') : [];
	const missing = 8 - left.length - right.length;
	const middle = Array(missing).fill('0000');

	const fullGroups = [
		...left.map((g) => g.padStart(4, '0')),
		...middle,
		...right.map((g) => g.padStart(4, '0'))
	];

	return fullGroups.join(':');
}

// Normalize IPv6 addresses for comparison
export function normalizeIPv6(ip: string): string {
	return expandIPv6(ip);
}

// Check if two IPs match (handles IPv6 variations)
export function ipMatches(ip1: string, ip2: string): boolean {
	if (ip1 === ip2) return true;

	if (ip1.includes(':') || ip2.includes(':')) {
		return normalizeIPv6(ip1) === normalizeIPv6(ip2);
	}

	return false;
}

// Check if IP is IPv6
export function isIPv6(ip: string): boolean {
	return ip.includes(':');
}

// Check if IP is IPv4
export function isIPv4(ip: string): boolean {
	return !ip.includes(':') && ip.split('.').length === 4;
}
