export interface Device {
	id: string;
	name: string;
	hostname: string;
	addresses: string[];
	os: string;
	online: boolean;
	lastSeen: string;
	tags: string[];
}

export interface NetworkLog {
	logged: string;
	nodeId: string;
	start: string;
	end: string;
	virtualTraffic: Traffic[];
	subnetTraffic: Traffic[];
	exitTraffic: Traffic[];
	physicalTraffic: Traffic[];
}

export interface Traffic {
	proto: string | number;
	src: string;
	dst: string;
	txPkts: number;
	txBytes: number;
	rxPkts?: number;
	rxBytes?: number;
}

export interface NetworkNode {
	id: string;
	label: string;
	addresses: string[];
	online: boolean;
	type: 'device' | 'external';
}

export interface NetworkEdge {
	id: string;
	source: string;
	target: string;
	protocol: string;
	bytes: number;
	packets: number;
	bidirectional?: boolean;
}

export interface VIPServiceInfo {
	name: string;
	addrs: string[];
}

export interface StaticRecordInfo {
	addrs: string[];
	comment?: string;
}

export interface ServicesResponse {
	services: Record<string, VIPServiceInfo>;
	records: Record<string, StaticRecordInfo>;
}
