import { describe, expect, it } from 'vitest';
import { nodeMatchesSearch, type SearchableNode } from './node-search';
import { buildNodeSearchIndex, indexAgreesWithSearch, searchNodeIds, type IndexedSearchNode } from './node-search-index';

function node(id: string, partial: SearchableNode): IndexedSearchNode {
	return { id, ...partial };
}

describe('node search index', () => {
	const nodes = [
		node('laptop', {
			displayName: 'laptop',
			user: 'alice@example.com',
			ips: ['100.64.0.8'],
			tags: ['tag:eng'],
			device: { name: 'laptop.example.ts.net', hostname: 'laptop', user: 'alice@example.com' }
		}),
		node('build', {
			displayName: 'build',
			user: 'ada@example.com',
			tags: ['tag:prod'],
			ips: ['100.64.0.21']
		}),
		node('untagged', { displayName: 'bare', ips: ['10.0.0.4'] })
	];

	it('agrees with the graph matcher for tags, logins, and names', () => {
		for (const query of ['', 'tag:prod', 'tag:', 'user@ada', 'user@', 'ada@example.com', 'Alice@Example.com', 'ip:100.64', 'laptop', 'prod']) {
			expect(indexAgreesWithSearch(nodes, query)).toBe(true);
		}
		expect(nodeMatchesSearch(nodes[1], 'ada@example.com')).toBe(true);
	});

	it('finds a tag and a creator login among 20k nodes', () => {
		const many: IndexedSearchNode[] = [];
		for (let i = 0; i < 20000; i++) {
			many.push(
				node(`n${i}`, {
					displayName: `host-${i}`,
					user: `user${i}@example.com`,
					ips: [`100.64.${(i >> 8) & 255}.${i & 255}`],
					tags: ['tag:fleet']
				})
			);
		}
		many.push(
			node('build', {
				displayName: 'build',
				user: 'ada@example.com',
				tags: ['tag:prod'],
				ips: ['100.90.0.21']
			})
		);
		const started = performance.now();
		const index = buildNodeSearchIndex(many);
		const tagged = searchNodeIds(index, 'tag:prod');
		const login = searchNodeIds(index, 'ada@example.com');
		const shorthand = searchNodeIds(index, 'user@ada');
		const elapsed = performance.now() - started;
		expect(tagged).toEqual(new Set(['build']));
		expect(login).toEqual(new Set(['build']));
		expect(shorthand?.has('build')).toBe(true);
		expect(elapsed).toBeLessThan(500);
		expect(indexAgreesWithSearch([many[0], many[many.length - 1]], 'tag:prod')).toBe(true);
	});
});
