import { describe, expect, it } from 'vitest';
import { nodeMatchesSearch, type SearchableNode } from './node-search';

function node(partial: SearchableNode): SearchableNode {
	return partial;
}

describe('nodeMatchesSearch user login', () => {
	const alice = node({
		displayName: 'laptop',
		user: 'alice@example.com',
		ips: ['100.64.0.8'],
		tags: ['tag:eng'],
		device: { name: 'laptop.example.ts.net', hostname: 'laptop', user: 'alice@example.com' }
	});

	it('matches a full email, ignoring case', () => {
		expect(nodeMatchesSearch(alice, 'alice@example.com')).toBe(true);
		expect(nodeMatchesSearch(alice, 'Alice@Example.COM')).toBe(true);
	});

	it('matches a partial email without treating @ or . as operators', () => {
		expect(nodeMatchesSearch(alice, 'alice@ex')).toBe(true);
		expect(nodeMatchesSearch(alice, 'example.com')).toBe(true);
		expect(nodeMatchesSearch(alice, 'alice@exampleXcom')).toBe(false);
	});

	it('matches a login that is only on the device record', () => {
		expect(
			nodeMatchesSearch(
				node({
					displayName: 'laptop',
					ips: ['100.64.0.8'],
					device: { user: 'alice@example.com', hostname: 'laptop' }
				}),
				'alice@example.com'
			)
		).toBe(true);
	});

	it('does not match a different login', () => {
		expect(nodeMatchesSearch(alice, 'bob@example.com')).toBe(false);
	});

	it('keeps the user@ prefix as a login search', () => {
		expect(nodeMatchesSearch(alice, 'user@alice')).toBe(true);
		expect(nodeMatchesSearch(alice, 'user@bob')).toBe(false);
		expect(nodeMatchesSearch(node({ user: 'user@example.com', displayName: 'laptop' }), 'user@example.com')).toBe(
			true
		);
	});

	it('still matches tags, names, and ip prefixes', () => {
		expect(nodeMatchesSearch(alice, 'tag:eng')).toBe(true);
		expect(nodeMatchesSearch(alice, 'ip:100.64')).toBe(true);
		expect(nodeMatchesSearch(alice, 'laptop.example.ts.net')).toBe(true);
		expect(nodeMatchesSearch(alice, '')).toBe(true);
	});
});
