<script lang="ts">
	import { ArrowUpRight, X } from 'lucide-svelte';
	import { auditTime, flowLink, relativeTime, shortHost } from '#lib/audit/format';
	import { expiryState, jsonList } from '#lib/audit/insights';
	import type { AuditNodeSummary, AuditWho } from '#lib/audit/types';
	import { hrefWithTailnet } from '#lib/services/tailnet-query';
	import { selectedTailnetId } from '#lib/stores/tailnet-store';

	let { summary, onopen, onclose }: { summary: AuditNodeSummary; onopen?: (eventId: number) => void; onclose?: () => void } = $props();

	const expiry = $derived(expiryState(summary));
	const tags = $derived(jsonList(summary.tags));
	const routes = $derived(jsonList(summary.routes));
	const autoRoutes = $derived(jsonList(summary.autoApprovedRoutes));
	const EXPIRY_TEXT = {
		disabled: 'Expiry disabled',
		expired: 'Expired',
		soon: 'Expires soon',
		ok: 'Expires',
		unknown: 'Not seen'
	} as const;
	const EXPIRY_CLASS = {
		disabled: 'text-amber-700 dark:text-amber-400',
		expired: 'text-red-700 dark:text-red-400',
		soon: 'text-amber-700 dark:text-amber-400',
		ok: '',
		unknown: 'text-muted-foreground'
	} as const;
</script>

{#snippet who(label: string, w: AuditWho | undefined)}
	<div class="min-w-0">
		<div class="text-[11px] text-muted-foreground">{label}</div>
		{#if w}
			<button class="block max-w-full truncate text-left hover:underline" title={auditTime(w.at)} onclick={() => onopen?.(w.eventId)}>
				{relativeTime(w.at)}{#if w.actor}<span class="text-muted-foreground"> · {w.actor}</span>{/if}
			</button>
		{:else}
			<div class="text-muted-foreground">—</div>
		{/if}
	</div>
{/snippet}

<section class="border-b border-border bg-secondary/20 px-3 py-3 text-xs sm:px-6" data-testid="audit-device-summary">
	<div class="mb-2 flex items-center justify-between gap-2">
		<div class="flex min-w-0 items-center gap-2">
			<h3 class="truncate text-sm font-semibold">{shortHost(summary.name || summary.id)}</h3>
			{#if summary.ephemeral}<span class="rounded bg-violet-500/15 px-1.5 text-[10px] font-medium text-violet-700 dark:text-violet-300">ephemeral</span>{/if}
			{#if summary.deleted}<span class="rounded bg-red-500/15 px-1.5 text-[10px] font-medium text-red-700 dark:text-red-300">removed</span>{/if}
			<span class="text-muted-foreground">{summary.events} events · {summary.actors.length} {summary.actors.length === 1 ? 'person or service' : 'people or services'}</span>
		</div>
		<div class="flex items-center gap-2">
			{#if summary.lastLogin}
				<a class="inline-flex items-center gap-1 rounded-md border border-border px-2 py-0.5 hover:bg-secondary" href={hrefWithTailnet(flowLink(summary.id, summary.lastLogin.at), $selectedTailnetId)}>
					<ArrowUpRight class="h-3 w-3" /> Flows at last login
				</a>
			{/if}
			<button class="rounded-md p-1 hover:bg-secondary" aria-label="Clear device" onclick={() => onclose?.()}><X class="h-3.5 w-3.5" /></button>
		</div>
	</div>
	<div class="grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-4 lg:grid-cols-6">
		{@render who('Added', summary.created)}
		{@render who('Approved', summary.approved)}
		{@render who('Last login', summary.lastLogin)}
		{@render who('Last logout', summary.lastLogout)}
		<div class="min-w-0">
			<div class="text-[11px] text-muted-foreground">Key expiry</div>
			<div class="truncate {EXPIRY_CLASS[expiry]}" title={summary.keyExpiry ? auditTime(summary.keyExpiry) : ''}>
				{EXPIRY_TEXT[expiry]}{#if expiry === 'ok' || expiry === 'soon'} {new Date(summary.keyExpiry ?? '').toLocaleDateString()}{/if}
			</div>
		</div>
		{@render who(summary.deleted ? 'Removed' : 'Tailnet lock signed', summary.deleted ?? summary.tailnetLockSigned)}
		<div class="col-span-2 min-w-0">
			<div class="text-[11px] text-muted-foreground">Tags{#if summary.tagsChanged} · changed {relativeTime(summary.tagsChanged.at)}{/if}</div>
			<div class="flex flex-wrap gap-1">
				{#each tags as tag}<span class="rounded bg-secondary px-1 font-mono text-[10px]">{tag}</span>{:else}<span class="text-muted-foreground">none logged</span>{/each}
			</div>
		</div>
		<div class="col-span-2 min-w-0">
			<div class="text-[11px] text-muted-foreground">Subnet routes</div>
			<div class="flex flex-wrap gap-1">
				{#each routes as r}<span class="rounded bg-secondary px-1 font-mono text-[10px]">{r}</span>{:else}<span class="text-muted-foreground">none logged</span>{/each}
				{#each autoRoutes as r}<span class="rounded bg-green-500/15 px-1 font-mono text-[10px]" title="Auto-approved">{r}</span>{/each}
			</div>
		</div>
		<div class="col-span-2 min-w-0 text-muted-foreground">
			<div class="text-[11px]">Other</div>
			{summary.renames} renames · {summary.postureChanges} posture changes
		</div>
	</div>
</section>
