import type { NetworkLink, NetworkNode } from '#lib/types';

// withLinkTotals recomputes each node's TX, RX, total, and connection count
// from the given links. The graph passes the links its traffic-type filter
// keeps, so a node card counts the same traffic as the edges and the header.
// Without this a card kept the all-types totals from processing, which add
// the physical (DERP or direct transport) bytes that carry the same virtual
// or subnet traffic a second time.
//
// Link accounting follows the processor: txBytes is what link.source sent to
// link.target, rxBytes is what came back. A self-link is one endpoint and its
// bytes all count as that node's TX.
export function withLinkTotals(nodes: NetworkNode[], links: NetworkLink[]): NetworkNode[] {
	const totals = new Map<string, { tx: number; rx: number; connections: number }>();
	const entry = (id: string) => {
		let total = totals.get(id);
		if (!total) {
			total = { tx: 0, rx: 0, connections: 0 };
			totals.set(id, total);
		}
		return total;
	};

	for (const link of links) {
		const source = entry(link.source);
		source.connections++;
		if (link.source === link.target) {
			source.tx += link.txBytes + link.rxBytes;
			continue;
		}
		const target = entry(link.target);
		target.connections++;
		source.tx += link.txBytes;
		source.rx += link.rxBytes;
		target.tx += link.rxBytes;
		target.rx += link.txBytes;
	}

	return nodes.map((node) => {
		const total = totals.get(node.id) ?? { tx: 0, rx: 0, connections: 0 };
		if (
			node.txBytes === total.tx &&
			node.rxBytes === total.rx &&
			node.totalBytes === total.tx + total.rx &&
			node.connections === total.connections
		) {
			return node;
		}
		return {
			...node,
			txBytes: total.tx,
			rxBytes: total.rx,
			totalBytes: total.tx + total.rx,
			connections: total.connections
		};
	});
}
