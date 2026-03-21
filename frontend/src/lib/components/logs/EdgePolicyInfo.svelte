<script lang="ts">
	import { Shield, ExternalLink } from 'lucide-svelte';
	import { uiStore, filteredNodes, filteredEdges } from '$lib/stores';
	import { policyGraph } from '$lib/stores/policy-store';
	import { matchPolicyRulesForEdge, type PolicyRuleMatch } from '$lib/stores/policy-traffic-store';

	const selectedEdge = $derived.by(() => {
		const edgeId = $uiStore.selectedEdgeId;
		if (!edgeId) return null;
		return $filteredEdges.find((e) => e.id === edgeId) ?? null;
	});

	const srcNode = $derived.by(() => {
		if (!selectedEdge) return null;
		return $filteredNodes.find((n) => n.id === selectedEdge.source) ?? null;
	});

	const dstNode = $derived.by(() => {
		if (!selectedEdge) return null;
		return $filteredNodes.find((n) => n.id === selectedEdge.target) ?? null;
	});

	const policyMatches = $derived.by((): PolicyRuleMatch[] => {
		if (!selectedEdge || !srcNode || !dstNode || !$policyGraph) return [];
		return matchPolicyRulesForEdge(
			selectedEdge,
			srcNode.tags ?? [],
			srcNode.user,
			dstNode.tags ?? [],
			dstNode.user,
			srcNode.ips ?? [],
			dstNode.ips ?? []
		);
	});

	const edgeTypeColor: Record<string, string> = {
		grant: '#0d9488',
		acl: '#0284c7',
		ssh: '#b45309'
	};

	function viewInPolicyGraph(selector: string) {
		window.location.href = `/policy?query=${encodeURIComponent(selector)}&direction=outbound`;
	}
</script>

{#if selectedEdge && $policyGraph}
	<div class="border-t border-border bg-card px-4 py-2">
		<div class="flex items-center justify-between">
			<div class="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
				<Shield class="h-3.5 w-3.5" />
				Policy Rules
			</div>
			{#if srcNode}
				<button
					onclick={() => viewInPolicyGraph(srcNode.tags?.[0] ?? srcNode.user ?? srcNode.id)}
					class="flex items-center gap-1 text-[10px] text-primary hover:underline"
				>
					View in Policy Graph
					<ExternalLink class="h-3 w-3" />
				</button>
			{/if}
		</div>

		{#if policyMatches.length > 0}
			<div class="mt-1.5 space-y-1">
				{#each policyMatches.slice(0, 5) as match}
					<div class="flex items-center gap-2 rounded bg-secondary/50 px-2 py-1 text-xs">
						<span
							class="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-semibold uppercase"
							style="color: {edgeTypeColor[match.edgeType] ?? '#9ca3af'}; background: color-mix(in srgb, {edgeTypeColor[match.edgeType] ?? '#9ca3af'} 15%, transparent)"
						>
							{match.edgeType}
						</span>
						<span class="truncate text-muted-foreground">
							{match.source} → {match.target}
						</span>
						{#if match.meta?.ports}
							<span class="shrink-0 text-muted-foreground/60">
								:{match.meta.ports.join(',')}
							</span>
						{/if}
					</div>
					{#if match.meta?.app && typeof match.meta.app === 'object'}
						<div class="ml-7 flex flex-wrap gap-1">
							{#each Object.entries(match.meta.app) as [cap, vals]}
								<span class="rounded bg-amber-500/15 px-1.5 py-0.5 text-[9px] text-amber-400" title={JSON.stringify(vals, null, 2)}>
									{cap.replace('tailscale.com/cap/', '').replace(/^.*\/cap\//, '')}
								</span>
							{/each}
						</div>
					{/if}
				{/each}
				{#if policyMatches.length > 5}
					<div class="text-[10px] text-muted-foreground">+{policyMatches.length - 5} more rules</div>
				{/if}
			</div>
		{:else}
			<div class="mt-1 text-xs text-muted-foreground/60">No matching policy rules found for this traffic.</div>
		{/if}
	</div>
{/if}
