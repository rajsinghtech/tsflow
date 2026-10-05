import type { DeviceScope } from '#lib/types';

export function hasDeviceScope(scope: DeviceScope | null | undefined): boolean {
	return scope != null;
}

// nodeMatchesDeviceScope is the traffic-view default. A null scope
// matches every node. A set scope matches an owner login exactly or
// a tag exactly, and otherwise matches nothing.
export function nodeMatchesDeviceScope(
	node: { user?: string; tags?: string[] },
	scope: DeviceScope | null | undefined
): boolean {
	if (scope == null) return true;

	const user = (node.user ?? '').trim().toLowerCase();
	for (const owner of scope.owners) {
		const want = owner.trim().toLowerCase();
		if (want !== '' && user === want) return true;
	}

	const tags = new Set((node.tags ?? []).map((tag) => tag.trim().toLowerCase()));
	for (const tag of scope.tags) {
		const raw = tag.trim().toLowerCase();
		if (!raw) continue;
		const bare = raw.startsWith('tag:') ? raw.slice(4) : raw;
		if (tags.has(raw) || tags.has(`tag:${bare}`) || tags.has(bare)) return true;
	}
	return false;
}

export function deviceScopeLabel(scope: DeviceScope | null | undefined): string {
	if (scope == null) return '';
	const parts = [...scope.owners, ...scope.tags].map((part) => part.trim()).filter(Boolean);
	if (parts.length === 0) return 'No matching devices';
	return parts.join(', ');
}
