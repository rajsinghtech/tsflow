<script lang="ts">
	import { ArrowLeft, ArrowUpRight, History, Info, Loader2 } from 'lucide-svelte';
	import { page } from '$app/state';
	import Header from '#lib/components/layout/Header.svelte';
	import AuditChange from '#lib/components/audit/AuditChange.svelte';
	import AuditUnavailable from '#lib/components/audit/AuditUnavailable.svelte';
	import { auditService } from '#lib/audit/audit-service';
	import { auditTime, shortHost, eventLabel, eventTone, flowLink, hasChange, isNodeTarget, relativeTime, targetLabel, actorLabel, TONE_CLASSES } from '#lib/audit/format';
	import type { AuditEvent, AuditLiveDevice, AuditLoginRef, AuditNodeRef, AuditUserResponse } from '#lib/audit/types';
	import { APIError } from '#lib/services/api-service';
	import { hrefWithTailnet } from '#lib/services/tailnet-query';
	import { selectedTailnetId } from '#lib/stores/tailnet-store';

	const login = $derived(decodeURIComponent(page.params.login ?? ''));

	let data = $state<AuditUserResponse | null>(null);
	let timeline = $state<AuditEvent[]>([]);
	let nextCursor = $state<string | undefined>(undefined);
	let loading = $state(true);
	let loadingMore = $state(false);
	let unavailable = $state<'disabled' | 'forbidden' | 'error' | null>(null);
	let open = $state<number | null>(null);
	let token = 0;

	async function load(who: string) {
		const mine = ++token;
		loading = true;
		unavailable = null;
		try {
			const res = await auditService.user(who);
			if (mine !== token) return;
			data = res;
			timeline = res.timeline.events ?? [];
			nextCursor = res.timeline.nextCursor;
		} catch (err) {
			if (mine !== token) return;
			data = null;
			unavailable = err instanceof APIError && err.status === 404 ? 'disabled' : err instanceof APIError && err.status === 403 ? 'forbidden' : 'error';
		} finally {
			if (mine === token) loading = false;
		}
	}

	async function more() {
		if (!nextCursor || loadingMore) return;
		loadingMore = true;
		try {
			const res = await auditService.events({ user: login, since: '400d', cursor: nextCursor, limit: 200 });
			timeline = [...timeline, ...res.events];
			nextCursor = res.nextCursor;
		} finally {
			loadingMore = false;
		}
	}

	$effect(() => {
		const who = login;
		void $selectedTailnetId;
		if (who) void load(who);
	});

	interface DeviceRow {
		id: string;
		name: string;
		os?: string;
		lastSeen?: string;
		connected?: boolean;
		lastLogin?: string;
		auditEvents: number;
	}

	// Live devices from the API plus devices seen only in the audit log
	// (for example ones deleted since).
	const devices = $derived.by<DeviceRow[]>(() => {
		if (!data) return [];
		const rows = new Map<string, DeviceRow>();
		for (const d of data.live.devices ?? ([] as AuditLiveDevice[])) {
			rows.set(d.id, { id: d.id, name: d.name, os: d.os, lastSeen: d.lastSeen, connected: d.connectedToControl, auditEvents: 0 });
		}
		for (const n of data.summary.nodes ?? ([] as AuditNodeRef[])) {
			const row = rows.get(n.id) ?? { id: n.id, name: n.name, auditEvents: 0 };
			row.lastLogin = n.lastLogin;
			row.auditEvents = n.events;
			if (!row.name) row.name = n.name;
			rows.set(n.id, row);
		}
		return [...rows.values()].sort((a, b) => (b.lastLogin ?? b.lastSeen ?? '').localeCompare(a.lastLogin ?? a.lastSeen ?? ''));
	});

	// Group the timeline by local day.
	const days = $derived.by(() => {
		const out: Array<{ day: string; events: AuditEvent[] }> = [];
		for (const e of timeline) {
			const day = new Date(e.eventTime).toLocaleDateString(undefined, { weekday: 'short', year: 'numeric', month: 'short', day: 'numeric' });
			if (out.length === 0 || out[out.length - 1].day !== day) out.push({ day, events: [] });
			out[out.length - 1].events.push(e);
		}
		return out;
	});

	const KIND_LABEL: Record<string, string> = {
		node: 'device login',
		admin_console: 'admin console',
		web_interface: 'web interface'
	};

	function deviceAt(ref: AuditLoginRef | undefined): string {
		if (!ref) return '';
		return ref.kind === 'node' ? shortHost(ref.target || ref.targetId || '') : '';
	}
</script>

<div class="flex h-dvh flex-col bg-background">
	<Header />
	<main class="flex-1 overflow-y-auto p-3 sm:p-6">
		<a class="mb-3 inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground" href={hrefWithTailnet('/audit', $selectedTailnetId)}>
			<ArrowLeft class="h-3.5 w-3.5" /> Audit log
		</a>

		{#if unavailable}
			<AuditUnavailable reason={unavailable} />
		{:else if loading && !data}
			<div class="flex justify-center py-16 text-muted-foreground"><Loader2 class="h-5 w-5 animate-spin" /></div>
		{:else if data}
			{@const s = data.summary}
			{@const live = data.live.user}
			<div class="mb-4 flex flex-wrap items-end justify-between gap-2">
				<div>
					<h2 class="text-base font-semibold">{s.displayName || live?.displayName || login}</h2>
					<p class="text-xs text-muted-foreground">
						{[login, live?.role, live?.status?.toLowerCase(), data.live.available ? '' : 'live users API unavailable'].filter(Boolean).join(' · ')}
					</p>
				</div>
				{#if s.firstSeen}
					<p class="text-xs text-muted-foreground">In the audit log since {auditTime(s.firstSeen)}</p>
				{/if}
			</div>

			<div class="mb-3 grid grid-cols-2 gap-2 lg:grid-cols-5" data-testid="audit-user-summary">
				{#snippet card(title: string, at: string | undefined, sub: string, highlight = false)}
					<div class="rounded-lg border p-3 {highlight ? 'border-primary/50 bg-primary/5' : 'border-border bg-card'}">
						<div class="text-xs text-muted-foreground">{title}</div>
						<div class="text-lg font-semibold tabular-nums">{at ? relativeTime(at) : 'never'}</div>
						<div class="truncate text-[11px] text-muted-foreground" title={at ? auditTime(at) : ''}>{at ? auditTime(at) : 'none in stored logs'}</div>
						{#if sub}<div class="truncate text-[11px] text-muted-foreground">{sub}</div>{/if}
					</div>
				{/snippet}
				{@render card('Last login', s.lastLogin?.eventTime, s.lastLogin ? (KIND_LABEL[s.lastLogin.kind] ?? s.lastLogin.kind) + (deviceAt(s.lastLogin) ? ` · ${deviceAt(s.lastLogin)}` : '') : '', true)}
				{@render card('Device login', s.lastNodeLogin?.eventTime, deviceAt(s.lastNodeLogin))}
				{@render card('Admin console', s.lastConsoleLogin?.eventTime, '')}
				{@render card('Web interface', s.lastWebLogin?.eventTime, deviceAt(s.lastWebLogin))}
				{@render card('Last seen (users API)', live?.lastSeen, live ? (live.currentlyConnected ? 'connected now' : `${live.deviceCount} devices`) : 'unavailable')}
			</div>
			<p class="mb-4 flex gap-2 rounded-md border border-border bg-secondary/30 px-3 py-2 text-xs text-muted-foreground">
				<Info class="mt-0.5 h-3.5 w-3.5 shrink-0" />
				<span>
					A device login is when one of this user's devices signed in to the tailnet (also re-auth after key expiry). Admin console is a
					sign-in to the Tailscale admin console. Users API last seen is Tailscale's own "last active" time and moves whenever a device is
					online, so it is often newer than any login. Audit counts: {s.actorEvents.toLocaleString()} changes made, {s.targetEvents.toLocaleString()}
					changes to this user.
				</span>
			</p>

			<div class="mb-4 rounded-lg border border-border bg-card p-3">
				<h3 class="mb-2 text-sm font-medium text-muted-foreground">Devices</h3>
				{#if devices.length === 0}
					<p class="text-sm text-muted-foreground">No devices for this user.</p>
				{:else}
					<div class="overflow-x-auto">
						<table class="w-full text-sm">
							<thead class="text-left text-[11px] uppercase tracking-wide text-muted-foreground">
								<tr>
									<th class="py-1 pr-3 font-medium">Device</th>
									<th class="py-1 pr-3 font-medium">OS</th>
									<th class="py-1 pr-3 font-medium">Last login (audit)</th>
									<th class="py-1 pr-3 font-medium">Last seen (API)</th>
									<th class="py-1 font-medium"></th>
								</tr>
							</thead>
							<tbody class="divide-y divide-border/60">
								{#each devices as d}
									<tr>
										<td class="py-1.5 pr-3">
											<span class="flex items-center gap-2">
												<span class="h-2 w-2 shrink-0 rounded-full {d.connected ? 'bg-green-500' : d.lastSeen ? 'bg-muted-foreground/40' : 'border border-muted-foreground/60'}" title={d.connected ? 'connected' : d.lastSeen ? 'offline' : 'not one of this user\'s devices now (deleted, or tagged)'}></span>
												<span class="truncate" title={d.name}>{shortHost(d.name || d.id)}</span>
												{#if !d.lastSeen}<span class="text-[11px] text-muted-foreground">only in audit log</span>{/if}
											</span>
										</td>
										<td class="py-1.5 pr-3 text-muted-foreground">{d.os ?? '—'}</td>
										<td class="py-1.5 pr-3 tabular-nums text-muted-foreground" title={auditTime(d.lastLogin)}>{d.lastLogin ? relativeTime(d.lastLogin) : '—'}</td>
										<td class="py-1.5 pr-3 tabular-nums text-muted-foreground" title={auditTime(d.lastSeen)}>{d.connected ? 'now' : d.lastSeen ? relativeTime(d.lastSeen) : '—'}</td>
										<td class="py-1.5 text-right text-xs whitespace-nowrap">
											{#if d.lastLogin || d.lastSeen}
												<a class="inline-flex items-center gap-0.5 text-primary hover:underline" href={hrefWithTailnet(flowLink(d.id, (d.lastLogin ?? d.lastSeen) as string), $selectedTailnetId)}>
													<ArrowUpRight class="h-3 w-3" /> Flows
												</a>
											{/if}
											<a class="ml-2 inline-flex items-center gap-0.5 text-primary hover:underline" href={hrefWithTailnet(`/audit?targetId=${encodeURIComponent(d.id)}&since=400d`, $selectedTailnetId)}>
												<History class="h-3 w-3" /> History
											</a>
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
			</div>

			<div class="rounded-lg border border-border bg-card p-3" data-testid="audit-user-timeline">
				<h3 class="mb-2 text-sm font-medium text-muted-foreground">Timeline</h3>
				{#if timeline.length === 0}
					<p class="text-sm text-muted-foreground">No audit events for this user.</p>
				{:else}
					{#each days as group}
						<div class="mb-3">
							<div class="sticky top-0 z-10 bg-card py-1 text-xs font-medium text-muted-foreground">{group.day}</div>
							<ol class="relative ml-2 border-l border-border">
								{#each group.events as e (e.id)}
									{@const tone = eventTone(e)}
									{@const byUser = (e.actor.loginName ?? '').toLowerCase() === login.toLowerCase()}
									<li class="ml-4 py-1.5">
										<span class="absolute -left-[5px] mt-1.5 h-2.5 w-2.5 rounded-full border-2 border-card {tone === 'login' ? 'bg-blue-500' : tone === 'create' ? 'bg-green-500' : tone === 'delete' || tone === 'error' ? 'bg-red-500' : tone === 'update' ? 'bg-amber-500' : 'bg-muted-foreground'}"></span>
										<button class="w-full text-left" onclick={() => (open = open === e.id ? null : e.id)}>
											<div class="flex flex-wrap items-baseline gap-x-2 text-sm">
												<span class="text-xs tabular-nums text-muted-foreground">{new Date(e.eventTime).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}</span>
												<span class="rounded px-1.5 py-0.5 text-xs font-medium {TONE_CLASSES[tone]}">{eventLabel(e.event)}</span>
												<span class="min-w-0 truncate">{targetLabel(e, true)}</span>
												{#if !byUser}<span class="text-xs text-muted-foreground">by {actorLabel(e)}</span>{/if}
												{#if e.origin}<span class="text-xs text-muted-foreground">· {e.origin.toLowerCase().replace(/_/g, ' ')}</span>{/if}
											</div>
										</button>
										{#if open === e.id}
											<div class="mt-2 space-y-2 rounded-md border border-border p-2">
												{#if isNodeTarget(e)}
													<a class="inline-flex items-center gap-1 text-xs text-primary hover:underline" href={hrefWithTailnet(flowLink(e.target.id ?? '', e.eventTime), $selectedTailnetId)}>
														<ArrowUpRight class="h-3 w-3" /> Flows for {targetLabel(e, true)} around this time
													</a>
												{/if}
												{#if hasChange(e)}
													<AuditChange event={e} />
												{:else}
													<p class="text-xs text-muted-foreground">{auditTime(e.eventTime)} · no old or new value recorded.</p>
												{/if}
											</div>
										{/if}
									</li>
								{/each}
							</ol>
						</div>
					{/each}
					{#if nextCursor}
						<button class="mt-1 rounded-md border border-border px-3 py-1 text-xs hover:bg-secondary" onclick={more} disabled={loadingMore}>
							{loadingMore ? 'Loading…' : 'Older events'}
						</button>
					{/if}
				{/if}
			</div>
		{/if}
	</main>
</div>
