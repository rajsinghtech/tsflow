<script lang="ts">
	import {
		visibility,
		toggleEdgeType,
		toggleNodeType,
		toggleHideIsolated,
		edgeTypeCounts
	} from '$lib/stores/policy-store';
	import type { NodeVisibilityOptions } from '$lib/stores/policy-store';
	import { NODE_COLORS } from '$lib/utils/policy-layout';

	const edgeTypes: { key: 'showGrantEdges' | 'showAclEdges' | 'showSshEdges' | 'showRelationEdges'; label: string; color: string; countKey: 'grant' | 'acl' | 'ssh' | 'relation' }[] = [
		{ key: 'showGrantEdges', label: 'Grants', color: '#0d9488', countKey: 'grant' },
		{ key: 'showAclEdges', label: 'ACLs', color: '#0284c7', countKey: 'acl' },
		{ key: 'showSshEdges', label: 'SSH', color: '#b45309', countKey: 'ssh' },
		{ key: 'showRelationEdges', label: 'Relations', color: '#9ca3af', countKey: 'relation' }
	];

	const nodeTypes: { key: keyof NodeVisibilityOptions; label: string; type: string }[] = [
		{ key: 'showUserNodes', label: 'Users', type: 'user' },
		{ key: 'showGroupNodes', label: 'Groups', type: 'group' },
		{ key: 'showTagNodes', label: 'Tags', type: 'tag' },
		{ key: 'showAutogroupNodes', label: 'Autogroups', type: 'autogroup' },
		{ key: 'showHostNodes', label: 'Hosts', type: 'host' },
		{ key: 'showIpsetNodes', label: 'IPsets', type: 'ipset' },
		{ key: 'showServiceNodes', label: 'Services', type: 'service' },
		{ key: 'showIpNodes', label: 'IPs', type: 'ip' },
		{ key: 'showCidrNodes', label: 'CIDRs', type: 'cidr' },
		{ key: 'showWildcardNodes', label: 'Wildcards', type: 'wildcard' }
	];
</script>

<div class="space-y-3">
	<div>
		<h3 class="mb-1.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Edge Types</h3>
		<div class="space-y-1">
			{#each edgeTypes as et}
				<label class="flex items-center gap-2 text-xs">
					<input type="checkbox" class="rounded border-input" checked={$visibility[et.key]} onchange={() => toggleEdgeType(et.key)} />
					<span class="flex items-center gap-1.5">
						<span class="h-2 w-2 rounded-sm" style="background: {et.color}"></span>
						{et.label}
					</span>
					<span class="ml-auto text-muted-foreground">{$edgeTypeCounts[et.countKey]}</span>
				</label>
			{/each}
		</div>
	</div>
	<div>
		<h3 class="mb-1.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Node Types</h3>
		<div class="flex flex-wrap gap-1">
			{#each nodeTypes as nt}
				<button
					class="rounded px-2 py-0.5 text-[10px] font-medium transition-opacity"
					class:opacity-40={!$visibility.nodeVisibility[nt.key]}
					style="background: color-mix(in srgb, {NODE_COLORS[nt.type]} 20%, transparent); color: {NODE_COLORS[nt.type]}"
					onclick={() => toggleNodeType(nt.key)}
				>
					{nt.label}
				</button>
			{/each}
		</div>
	</div>
	<label class="flex items-center gap-2 text-xs text-muted-foreground">
		<input type="checkbox" class="rounded border-input" checked={$visibility.hideIsolatedNodes} onchange={toggleHideIsolated} />
		Hide isolated nodes
	</label>
</div>
