import type { Protocol } from '#lib/types';

// Protocol number to name mapping
export function getProtocolName(proto: number): Protocol {
	switch (proto) {
		case 1:
			return 'icmp';
		case 6:
			return 'tcp';
		case 17:
			return 'udp';
		default:
			return 'other';
	}
}

const WELL_KNOWN_PORTS: Record<number, string> = {
	21: 'FTP',
	22: 'SSH',
	25: 'SMTP',
	53: 'DNS',
	80: 'HTTP',
	110: 'POP3',
	143: 'IMAP',
	443: 'HTTPS',
	465: 'SMTPS',
	587: 'Submission',
	636: 'LDAPS',
	853: 'DoT',
	993: 'IMAPS',
	995: 'POP3S',
	1433: 'MSSQL',
	1521: 'Oracle',
	2049: 'NFS',
	2379: 'etcd',
	2380: 'etcd-peer',
	3000: 'Grafana',
	3306: 'MySQL',
	3389: 'RDP',
	4222: 'NATS',
	4443: 'Pharos',
	5060: 'SIP',
	5432: 'PostgreSQL',
	5672: 'AMQP',
	5900: 'VNC',
	6379: 'Redis',
	6443: 'K8s API',
	7233: 'Temporal',
	8080: 'HTTP-Alt',
	8443: 'HTTPS-Alt',
	8500: 'Consul',
	8888: 'HTTP-Proxy',
	9000: 'gRPC-Alt',
	9090: 'Prometheus',
	9093: 'Alertmanager',
	9100: 'Node Exp',
	9200: 'Elasticsearch',
	9418: 'Git',
	10250: 'Kubelet',
	11211: 'Memcached',
	15672: 'RabbitMQ',
	27017: 'MongoDB',
	41641: 'Tailscale',
};

export function getPortName(port: number): string {
	return WELL_KNOWN_PORTS[port] || `${port}`;
}

export function getPortLabel(port: number, proto: number): string {
	const name = WELL_KNOWN_PORTS[port];
	const protoName = getProtocolName(proto).toUpperCase();
	return name ? `${name} (${port}/${protoName})` : `${port}/${protoName}`;
}
