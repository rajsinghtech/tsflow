<script lang="ts">
	import { FileCode2, KeyRound, Settings2, Users } from 'lucide-svelte';
	import { actorTypeLabel, auditTime, keyKindLabel, originLabel, relativeTime } from '#lib/audit/format';
	import { keyFlags } from '#lib/audit/insights';
	import type { AuditFacets, AuditInsights } from '#lib/audit/types';

	let {
		insights,
		facets,
		onopen,
		onactor,
		ontarget
	}: {
		insights: AuditInsights;
		facets: AuditFacets | null;
		onopen?: (eventId: number) => void;
		onactor?: (actor: string) => void;
		ontarget?: (id: string) => void;
	} = $props();

	const STATUS_CLASS: Record<string, string> = {
		active: 'bg-green-500/15 text-green-700 dark:text-green-300',
		revoked: 'bg-red-500/15 text-red-700 dark:text-red-300',
		expired: 'bg-secondary text-muted-foreground',
		unknown: 'bg-secondary text-muted-foreground'
	};
	const STATE_CLASS: Record<string, string> = {
		enabled: 'text-green-700 dark:text-green-400',
		added: 'text-green-700 dark:text-green-400',
		verified: 'text-green-700 dark:text-green-400',
		disabled: 'text-red-700 dark:text-red-400',
		removed: 'text-red-700 dark:text-red-400'
	};
</script>

<div class="min-h-0 flex-1 overflow-y-auto p-3 sm:p-6" data-testid="audit-overview">
	<div class="grid gap-4 xl:grid-cols-2">
		<section class="rounded-lg border border-border">
			<h3 class="flex items-center gap-2 border-b border-border px-3 py-2 text-sm font-semibold">
				<FileCode2 class="h-4 w-4" /> Policy file edits
				<span class="font-normal text-muted-foreground">({insights.policy.length})</span>
			</h3>
			{#if insights.policy.length === 0}
				<p class="px-3 py-4 text-xs text-muted-foreground">No policy file edits in this range.</p>
			{:else}
				<table class="w-full text-xs">
					<thead class="text-left text-[11px] text-muted-foreground">
						<tr><th class="px-3 py-1.5 font-medium">When</th><th class="font-medium">Who</th><th class="font-medium">Via</th><th class="pr-3 text-right font-medium">Lines</th></tr>
					</thead>
					<tbody>
						{#each insights.policy as p (p.eventId)}
							<tr class="cursor-pointer border-t border-border/60 hover:bg-secondary/40" onclick={() => onopen?.(p.eventId)}>
								<td class="px-3 py-1.5 tabular-nums" title={auditTime(p.at)}>{relativeTime(p.at)}</td>
								<td class="max-w-48 truncate">{p.actor || '—'}</td>
								<td class="text-muted-foreground">{originLabel(p.origin)}</td>
								<td class="pr-3 text-right tabular-nums">
									{#if p.error}<span class="text-red-600 dark:text-red-400" title={p.error}>failed</span>
									{:else}<span class="text-green-700 dark:text-green-400">+{p.added}</span> <span class="text-red-700 dark:text-red-400">−{p.removed}</span>{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			{/if}
		</section>

		<section class="rounded-lg border border-border">
			<h3 class="flex items-center gap-2 border-b border-border px-3 py-2 text-sm font-semibold">
				<Settings2 class="h-4 w-4" /> Tailnet settings changed
				<span class="font-normal text-muted-foreground">({insights.settings.length})</span>
			</h3>
			{#if insights.settings.length === 0}
				<p class="px-3 py-4 text-xs text-muted-foreground">No tailnet settings changed in this range.</p>
			{:else}
				<table class="w-full text-xs">
					<thead class="text-left text-[11px] text-muted-foreground">
						<tr><th class="px-3 py-1.5 font-medium">Setting</th><th class="font-medium">Now</th><th class="font-medium">Last change</th><th class="pr-3 text-right font-medium">Changes</th></tr>
					</thead>
					<tbody>
						{#each insights.settings as s (s.event)}
							<tr class="cursor-pointer border-t border-border/60 hover:bg-secondary/40" onclick={() => s.last && onopen?.(s.last.eventId)}>
								<td class="max-w-56 truncate px-3 py-1.5" title={s.event}>{s.label}</td>
								<td class={STATE_CLASS[s.state] ?? ''}>{s.state}</td>
								<td class="max-w-48 truncate text-muted-foreground" title={auditTime(s.at)}>{relativeTime(s.at)}{#if s.last?.actor} · {s.last.actor}{/if}</td>
								<td class="pr-3 text-right tabular-nums">{s.changes}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			{/if}
		</section>

		<section class="rounded-lg border border-border xl:col-span-2">
			<h3 class="flex items-center gap-2 border-b border-border px-3 py-2 text-sm font-semibold">
				<KeyRound class="h-4 w-4" /> Keys and OAuth clients
				<span class="font-normal text-muted-foreground">({insights.keys.length})</span>
			</h3>
			{#if insights.keys.length === 0}
				<p class="px-3 py-4 text-xs text-muted-foreground">No key activity in this range.</p>
			{:else}
				<div class="overflow-x-auto">
					<table class="w-full min-w-[46rem] text-xs">
						<thead class="text-left text-[11px] text-muted-foreground">
							<tr>
								<th class="px-3 py-1.5 font-medium">Key</th><th class="font-medium">Kind</th><th class="font-medium">Status</th><th class="font-medium">Created</th>
								<th class="font-medium">Ended</th><th class="font-medium">Grants</th><th class="pr-3 font-medium">Expires</th>
							</tr>
						</thead>
						<tbody>
							{#each insights.keys as k (k.id)}
								{@const ended = k.revoked ?? k.expired}
								<tr class="border-t border-border/60 hover:bg-secondary/40">
									<td class="max-w-48 px-3 py-1.5">
										<button class="block max-w-full truncate text-left hover:underline" onclick={() => ontarget?.(k.id)} title="Show this key's events">{k.name || k.description || k.id}</button>
										{#if k.name && k.name !== k.id}<div class="truncate font-mono text-[10px] text-muted-foreground">{k.id}</div>{/if}
									</td>
									<td>{keyKindLabel(k.kind)}</td>
									<td><span class="rounded px-1.5 py-0.5 text-[10px] font-medium {STATUS_CLASS[k.status] ?? STATUS_CLASS.unknown}">{k.status}</span></td>
									<td class="max-w-40 truncate">
										{#if k.created}<button class="hover:underline" title={auditTime(k.created.at)} onclick={() => k.created && onopen?.(k.created.eventId)}>{relativeTime(k.created.at)}{#if k.created.actor} · {k.created.actor}{/if}</button>{:else}<span class="text-muted-foreground" title="Created before the stored history">earlier</span>{/if}
									</td>
									<td class="max-w-40 truncate">
										{#if ended}<button class="hover:underline" title={auditTime(ended.at)} onclick={() => onopen?.(ended.eventId)}>{relativeTime(ended.at)}{#if ended.actor} · {ended.actor}{/if}</button>{:else}<span class="text-muted-foreground">—</span>{/if}
									</td>
									<td class="max-w-64">
										<div class="flex flex-wrap gap-1">
											{#each k.tags ?? [] as tag}<span class="rounded bg-secondary px-1 font-mono text-[10px]">{tag}</span>{/each}
											{#each k.scopes ?? [] as scope}<span class="rounded bg-blue-500/10 px-1 font-mono text-[10px]">{scope}</span>{/each}
											{#each keyFlags(k) as flag}<span class="rounded bg-amber-500/15 px-1 text-[10px]">{flag}</span>{/each}
										</div>
									</td>
									<td class="pr-3 tabular-nums text-muted-foreground">{k.expires ? new Date(k.expires).toLocaleDateString() : '—'}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
				<p class="border-t border-border/60 px-3 py-1.5 text-[11px] text-muted-foreground">Tailscale logs auth keys, OAuth clients and federated identities all as "API key"; the kind shown is worked out from what the key can do.</p>
			{/if}
		</section>

		{#if facets}
			<section class="rounded-lg border border-border xl:col-span-2">
				<h3 class="flex items-center gap-2 border-b border-border px-3 py-2 text-sm font-semibold"><Users class="h-4 w-4" /> Most active</h3>
				<div class="grid gap-4 p-3 text-xs md:grid-cols-3">
					{#snippet top(title: string, items: AuditFacets['actors'], pick: ((v: string) => void) | undefined, label: (v: string, l?: string) => string)}
						{@const max = Math.max(1, ...items.map((i) => i.count))}
						<div>
							<h4 class="mb-1 font-medium text-muted-foreground">{title}</h4>
							{#each items.slice(0, 8) as item}
								<button class="relative mb-0.5 flex w-full items-center justify-between gap-2 overflow-hidden rounded px-1.5 py-0.5 text-left hover:bg-secondary/60" onclick={() => pick?.(item.value)} title={item.value}>
									<span class="absolute inset-y-0 left-0 bg-primary/10" style="width: {(item.count / max) * 100}%"></span>
									<span class="relative truncate">{label(item.value, item.label)}</span>
									<span class="relative tabular-nums text-muted-foreground">{item.count}</span>
								</button>
							{:else}<p class="text-muted-foreground">None</p>{/each}
						</div>
					{/snippet}
					{@render top('Who made changes', facets.actors, onactor, (v, l) => l || v)}
					{@render top('What changed most', facets.targets, ontarget, (v, l) => l || v)}
					{@render top('Kind of actor', facets.actorTypes, undefined, (v) => actorTypeLabel(v))}
				</div>
			</section>
		{/if}
	</div>
</div>
