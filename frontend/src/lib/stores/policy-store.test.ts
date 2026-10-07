import '#lib/stores/ui-store';
import { describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import { fetchAndRenderPolicy, fetchError, policyGraph } from '#lib/stores/policy-store';

describe('fetchAndRenderPolicy', () => {
	it('keeps the backend reason when the policy cannot be fetched', async () => {
		// A key without the policy scope is a common setup. The page needs the
		// backend's reason, not only the HTTP status text.
		globalThis.fetch = (async (input: RequestInfo | URL) => {
			const url = String(input);
			if (url.includes('/api/tailnets')) {
				return new Response(JSON.stringify({ tailnets: [{ id: 'default', displayName: 'example.com' }] }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				});
			}
			return new Response(JSON.stringify({ error: 'status 403: {"message":"missing scope policy_file:read"}' }), {
				status: 500,
				statusText: 'Internal Server Error',
				headers: { 'Content-Type': 'application/json' }
			});
		}) as typeof fetch;

		await fetchAndRenderPolicy();

		expect(get(policyGraph)).toBeNull();
		expect(get(fetchError)).toContain('missing scope policy_file:read');
	});
});
