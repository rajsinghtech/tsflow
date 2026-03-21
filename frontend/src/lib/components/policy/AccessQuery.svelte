<script lang="ts">
	import { X } from 'lucide-svelte';
	import { queryText, queryDirection, queryResult, runQuery, clearQuery, policyGraph } from '$lib/stores/policy-store';
	import type { AccessEdgeMeta } from '$lib/policy-engine/types';

	let selectorInput = $state($queryText);
	let direction = $state<'inbound' | 'outbound'>($queryDirection);

	function handleQuery() {
		if (!selectorInput.trim()) return;
		runQuery(selectorInput.trim(), direction);
	}

	function handleClear() {
		selectorInput = '';
		clearQuery();
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter') handleQuery();
	}

	function setDirection(d: 'inbound' | 'outbound') {
		direction = d;
		if (selectorInput.trim()) {
			runQuery(selectorInput.trim(), d);
		}
	}

	const edgeColor: Record<string, string> = {
		grant: '#0d9488',
		acl: '#0284c7',
		ssh: '#b45309'
	};

	// Deduplicate matches: group by type + peer node, count duplicates
	interface AppCapDetail {
		name: string;
		shortName: string;
		values: any[];
	}

	interface GroupedMatch {
		edgeType: string;
		peer: string;
		fullLabel: string;
		appCaps: AppCapDetail[];
		ports?: string[];
		ip?: string[];
		count: number;
	}

	function formatCapValue(val: any): string {
		if (typeof val === 'string') return val;
		if (typeof val !== 'object' || val === null) return JSON.stringify(val);
		// Pretty-print key fields
		const parts: string[] = [];
		if (val.routes) parts.push(`routes: ${val.routes.join(', ')}`);
		if (val.shares) parts.push(`shares: ${val.shares.join(', ')} (${val.access ?? 'ro'})`);
		if (val.impersonate?.groups) parts.push(`groups: ${val.impersonate.groups.join(', ')}`);
		if (val.enforceRecorder) parts.push('recorded');
		if (val.allow_admin_ui) parts.push('admin UI');
		if (val.access) {
			const dbs = val.access.map((a: any) => a.databases?.join(',')).filter(Boolean);
			if (dbs.length) parts.push(`dbs: ${dbs.join('; ')}`);
		}
		// DNS proxy entries
		const dnsKeys = Object.keys(val).filter((k) => val[k]?.dns);
		if (dnsKeys.length) parts.push(`dns: ${dnsKeys.join(', ')}`);
		if (parts.length) return parts.join(' · ');
		return JSON.stringify(val).slice(0, 80);
	}

	const groupedMatches = $derived.by((): GroupedMatch[] => {
		if (!$queryResult || $queryResult.matches.length === 0 || !$policyGraph) return [];

		const edgeMap = new Map($policyGraph.edges.map((e) => [e.id, e]));

		const map = new Map<string, GroupedMatch>();
		for (const m of $queryResult.matches) {
			const peer = direction === 'outbound' ? m.targetNodeId : m.sourceNodeId;
			const key = `${m.edgeType}|${m.sourceNodeId}|${m.targetNodeId}`;
			const existing = map.get(key);
			if (existing) {
				existing.count++;
			} else {
				const edge = edgeMap.get(m.edgeId);
				const meta = edge?.meta as AccessEdgeMeta | undefined;
				const app = (meta as any)?.app;
				const caps: AppCapDetail[] = app && typeof app === 'object'
					? Object.entries(app).map(([name, vals]) => ({
						name,
						shortName: name.replace('tailscale.com/cap/', '').replace(/^.*\/cap\//, ''),
						values: Array.isArray(vals) ? vals : []
					}))
					: [];

				map.set(key, {
					edgeType: m.edgeType,
					peer,
					fullLabel: `${m.sourceNodeId} → ${m.targetNodeId}`,
					appCaps: caps,
					ports: meta?.ports,
					ip: meta?.ip,
					count: 1
				});
			}
		}
		return [...map.values()];
	});
</script>

{#if $policyGraph}
	<div class="space-y-2">
		<h3 class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Access Query</h3>
		<div class="relative">
			<input
				type="text"
				class="w-full rounded-md border border-input bg-background py-1.5 pl-2 pr-7 text-xs"
				placeholder="user@example.com, tag:server..."
				bind:value={selectorInput}
				onkeydown={handleKeydown}
			/>
			{#if selectorInput}
				<button onclick={handleClear} class="absolute right-1.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground">
					<X class="h-3 w-3" />
				</button>
			{/if}
		</div>
		<div class="flex gap-1">
			<button
				class="flex-1 rounded-md px-2 py-1 text-xs font-medium transition-colors {direction === 'outbound' ? 'bg-primary/20 text-primary border border-primary' : 'border border-border text-muted-foreground hover:bg-secondary'}"
				onclick={() => setDirection('outbound')}
			>
				→ Outbound
			</button>
			<button
				class="flex-1 rounded-md px-2 py-1 text-xs font-medium whitespace-nowrap transition-colors {direction === 'inbound' ? 'bg-primary/20 text-primary border border-primary' : 'border border-border text-muted-foreground hover:bg-secondary'}"
				onclick={() => setDirection('inbound')}
			>
				← Inbound
			</button>
		</div>

		{#if groupedMatches.length > 0}
			<div class="space-y-1 pt-1">
				{#each groupedMatches as gm}
					<div class="rounded bg-secondary/50 px-2 py-1.5 text-xs">
						<div class="flex items-start gap-1">
							<span
								class="mt-0.5 inline-block shrink-0 rounded px-1 py-0.5 text-[10px] font-semibold uppercase"
								style="color: {edgeColor[gm.edgeType] ?? '#9ca3af'}; background: color-mix(in srgb, {edgeColor[gm.edgeType] ?? '#9ca3af'} 15%, transparent)"
							>{gm.edgeType}</span>
							<span class="text-muted-foreground">{gm.fullLabel}</span>
						</div>
						{#if gm.ip?.length && !gm.ip.includes('*')}
							<div class="mt-1 text-[10px] text-muted-foreground/70">
								proto: {gm.ip.join(', ')}
							</div>
						{/if}
						{#if gm.ports?.length}
							<div class="mt-0.5 text-[10px] text-muted-foreground/70">
								ports: {gm.ports.join(', ')}
							</div>
						{/if}
						{#if gm.appCaps.length > 0}
							<div class="mt-1 space-y-1">
								{#each gm.appCaps as cap}
									<div class="rounded border border-amber-500/20 bg-amber-500/10 px-1.5 py-1">
										<div class="text-[10px] font-semibold text-amber-400">{cap.shortName}</div>
										{#each cap.values as val}
											<div class="mt-0.5 text-[9px] text-amber-300/70">{formatCapValue(val)}</div>
										{/each}
									</div>
								{/each}
							</div>
						{/if}
					</div>
				{/each}
				<div class="pt-1 text-[10px] text-muted-foreground/60">{groupedMatches.length} unique rules ({$queryResult?.matches.length ?? 0} total)</div>
			</div>
		{:else if $queryResult}
			<div class="text-xs text-muted-foreground">No matches found.</div>
		{/if}
	</div>
{/if}
