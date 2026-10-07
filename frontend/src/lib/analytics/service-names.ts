import { extractIP, ipMatches } from '#lib/utils/ip-utils';

export type ServiceMap = Record<string, { name: string; addrs: string[]; tags?: string[] }>;
export type RecordMap = Record<string, { addrs: string[]; comment?: string }>;

// serviceNameResolver names an address from the tailnet's VIP services and DNS
// records (/services-records). It returns '' when nothing claims the address,
// so device ids and unclaimed addresses keep the name the backend gave them.
export function serviceNameResolver(services: ServiceMap, records: RecordMap): (nodeId: string) => string {
	return (nodeId: string) => {
		const ip = extractIP(nodeId);
		if (!ip) return '';
		for (const [serviceName, service] of Object.entries(services)) {
			if (service.addrs?.some((addr) => ipMatches(ip, addr))) {
				return service.name || serviceName;
			}
		}
		for (const [recordName, record] of Object.entries(records)) {
			if (record.addrs?.some((addr) => ipMatches(ip, addr))) {
				return recordName;
			}
		}
		return '';
	};
}
