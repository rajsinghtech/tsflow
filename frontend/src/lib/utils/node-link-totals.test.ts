import { describe, expect, it } from 'vitest';
import type { NetworkLink, NetworkNode, TrafficType } from '#lib/types';
import { withLinkTotals } from './node-link-totals';

function node(id: string, extra: Partial<NetworkNode> = {}): NetworkNode {
	return {
		id,
		ip: '',
		displayName: id,
		nodeType: 'ip',
		totalBytes: 0,
		txBytes: 0,
		rxBytes: 0,
		connections: 0,
		tags: [],
		isTailscale: true,
		ips: [],
		incomingPorts: new Set(),
		outgoingPorts: new Set(),
		protocols: new Set(),
		isVIPService: false,
		...extra
	};
}

function link(source: string, target: string, txBytes: number, rxBytes: number, trafficType: TrafficType): NetworkLink {
	return {
		id: `${source}<->${target}|${trafficType}`,
		source,
		target,
		originalSource: source,
		originalTarget: target,
		totalBytes: txBytes + rxBytes,
		txBytes,
		rxBytes,
		packets: 0,
		protocol: 'tcp',
		trafficType,
		ports: new Set()
	};
}

describe('withLinkTotals', () => {
	it('counts each link once per endpoint in both directions', () => {
		const [a, b] = withLinkTotals([node('a'), node('b')], [link('a', 'b', 30, 70, 'virtual')]);
		expect([a.txBytes, a.rxBytes, a.totalBytes, a.connections]).toEqual([30, 70, 100, 1]);
		expect([b.txBytes, b.rxBytes, b.totalBytes, b.connections]).toEqual([70, 30, 100, 1]);
	});

	it('leaves out links the caller filtered away', () => {
		// The processed node still carries the physical transport bytes.
		const laptop = node('laptop', { txBytes: 400, rxBytes: 10900, totalBytes: 11300, connections: 3 });
		const kept = [link('10.20.0.5', 'laptop', 5000, 100, 'subnet'), link('laptop', 'server', 300, 700, 'virtual')];
		const [counted] = withLinkTotals([laptop], kept);
		expect([counted.txBytes, counted.rxBytes, counted.totalBytes, counted.connections]).toEqual([400, 5700, 6100, 2]);
	});

	it('counts a self-link once as transmit', () => {
		const [self] = withLinkTotals([node('a')], [link('a', 'a', 50, 0, 'virtual')]);
		expect([self.txBytes, self.rxBytes, self.totalBytes, self.connections]).toEqual([50, 0, 50, 1]);
	});

	it('returns the same object when nothing changes', () => {
		const a = node('a', { txBytes: 30, rxBytes: 70, totalBytes: 100, connections: 1 });
		const [out] = withLinkTotals([a], [link('a', 'b', 30, 70, 'virtual')]);
		expect(out).toBe(a);
	});
});
