<script lang="ts">
	import { ArrowUpRight, History, Loader2, User } from 'lucide-svelte';
	import { auditService } from '#lib/audit/audit-service';
	import {
		actorLabel,
		auditTime,
		eventLabel,
		eventTone,
		flowLink,
		isNodeTarget,
		relativeTime,
		targetLabel,
		TONE_CLASSES,
		userLogin
	} from '#lib/audit/format';
	import type { AuditEvent } from '#lib/audit/types';
	import { hrefWithTailnet } from '#lib/services/tailnet-query';
	import { selectedTailnetId } from '#lib/stores/tailnet-store';
	import AuditChange from './AuditChange.svelte';

	let { event, onselect }: { event: AuditEvent; onselect?: (event: AuditEvent) => void } = $props();

	let group = $state<AuditEvent[]>([]);
	let loadingGroup = $state(false);
	let loadedFor = 0;

	$effect(() => {
		const id = event.id;
		if (id === loadedFor) return;
		loadedFor = id;
		group = [];
		if (!event.eventGroupID) return;
		loadingGroup = true;
		auditService
			.event(id)
			.then((res) => {
				if (loadedFor === id) group = (res.group ?? []).filter((e) => e.id !== id);
			})
			.catch(() => {
				if (loadedFor === id) group = [];
			})
			.finally(() => {
				if (loadedFor === id) loadingGroup = false;
			});
	});

	const login = $derived(userLogin(event));
	const tone = $derived(eventTone(event));
</script>

<div class="space-y-4 text-sm">
	<div>
		<span class="inline-flex rounded px-1.5 py-0.5 text-xs font-medium {TONE_CLASSES[tone]}">{eventLabel(event.event)}</span>
		<div class="mt-1 font-mono text-[11px] text-muted-foreground">{event.event}</div>
		<div class="mt-1 text-xs text-muted-foreground">{auditTime(event.eventTime)} · {relativeTime(event.eventTime)}</div>
	</div>

	{#if event.error}
		<p class="rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2 text-xs text-red-700 dark:text-red-300">Failed: {event.error}</p>
	{/if}

	<dl class="grid grid-cols-[6rem_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-xs">
		<dt class="text-muted-foreground">Who</dt>
		<dd class="min-w-0">
			<div class="truncate font-medium">{actorLabel(event)}</div>
			{#if event.actor.displayName && event.actor.displayName !== event.actor.loginName}
				<div class="truncate text-muted-foreground">{event.actor.displayName}</div>
			{/if}
			{#if event.actor.type}<div class="text-muted-foreground">{event.actor.type.toLowerCase()}</div>{/if}
		</dd>
		<dt class="text-muted-foreground">What</dt>
		<dd class="min-w-0">
			<div class="truncate font-medium">{targetLabel(event) || '—'}</div>
			<div class="text-muted-foreground">
				{(event.target.type ?? '').toLowerCase()}{event.target.property ? ` · ${event.target.property.toLowerCase()}` : ''}{event.target.isEphemeral ? ' · ephemeral' : ''}
			</div>
			{#if event.target.id && event.target.id !== event.target.name}
				<div class="truncate font-mono text-[11px] text-muted-foreground">{event.target.id}</div>
			{/if}
		</dd>
		{#if event.origin}
			<dt class="text-muted-foreground">From</dt>
			<dd>{event.origin.toLowerCase().replace(/_/g, ' ')}</dd>
		{/if}
		{#if event.actionDetails}
			<dt class="text-muted-foreground">Details</dt>
			<dd class="break-words">{event.actionDetails}</dd>
		{/if}
	</dl>

	<div class="flex flex-wrap gap-2 text-xs">
		{#if login}
			<a
				class="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 hover:bg-secondary"
				href={hrefWithTailnet(`/audit/user/${encodeURIComponent(login)}`, $selectedTailnetId)}
			>
				<User class="h-3.5 w-3.5" /> {login}
			</a>
		{/if}
		{#if isNodeTarget(event)}
			<a
				class="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 hover:bg-secondary"
				href={hrefWithTailnet(flowLink(event.target.id ?? '', event.eventTime), $selectedTailnetId)}
				title="Traffic for this device an hour either side of the event"
			>
				<ArrowUpRight class="h-3.5 w-3.5" /> Flows around this time
			</a>
			<a
				class="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 hover:bg-secondary"
				href={hrefWithTailnet(`/audit?targetId=${encodeURIComponent(event.target.id ?? '')}&since=400d`, $selectedTailnetId)}
			>
				<History class="h-3.5 w-3.5" /> Device audit history
			</a>
		{/if}
	</div>

	<div>
		<h3 class="mb-1.5 text-xs font-medium text-muted-foreground">Change</h3>
		<AuditChange {event} />
	</div>

	{#if event.eventGroupID}
		<div>
			<h3 class="mb-1.5 flex items-center gap-2 text-xs font-medium text-muted-foreground">
				Same operation
				{#if loadingGroup}<Loader2 class="h-3 w-3 animate-spin" />{/if}
			</h3>
			{#if group.length === 0 && !loadingGroup}
				<p class="text-xs text-muted-foreground">No other events in this operation.</p>
			{:else}
				<ul class="divide-y divide-border/60 rounded-md border border-border text-xs">
					{#each group as other}
						<li>
							<button class="flex w-full items-center justify-between gap-2 px-2 py-1.5 text-left hover:bg-secondary/50" onclick={() => onselect?.(other)}>
								<span class="truncate">{eventLabel(other.event)} · {targetLabel(other, true)}</span>
								<span class="shrink-0 text-muted-foreground">{relativeTime(other.eventTime)}</span>
							</button>
						</li>
					{/each}
				</ul>
			{/if}
		</div>
	{/if}
</div>
