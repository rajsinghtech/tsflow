<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { AlertTriangle, Download, Filter, Loader2, RefreshCw, Search, X } from 'lucide-svelte';
	import Header from '#lib/components/layout/Header.svelte';
	import AuditEventDetail from '#lib/components/audit/AuditEventDetail.svelte';
	import AuditUnavailable from '#lib/components/audit/AuditUnavailable.svelte';
	import AuditSourceCard from '#lib/components/audit/AuditSourceCard.svelte';
	import AuditHistogram from '#lib/components/audit/AuditHistogram.svelte';
	import AuditOverview from '#lib/components/audit/AuditOverview.svelte';
	import AuditDeviceSummary from '#lib/components/audit/AuditDeviceSummary.svelte';
	import { auditService, loadCatalog } from '#lib/audit/audit-service';
	import {
		actorLabel,
		actorTypeLabel,
		auditTime,
		categoryLabel,
		eventLabel,
		eventTone,
		originLabel,
		relativeTime,
		targetLabel,
		TONE_CLASSES
	} from '#lib/audit/format';
	import type {
		AuditCategory,
		AuditEvent,
		AuditFacets,
		AuditInsights,
		AuditNodeSummary,
		AuditQuery,
		AuditStatus,
		AuditTailnetStatus
	} from '#lib/audit/types';
	import { APIError } from '#lib/services/api-service';
	import { hrefWithTailnet } from '#lib/services/tailnet-query';
	import { selectedTailnetId } from '#lib/stores/tailnet-store';

	const RANGES = [
		{ value: '24h', label: '24 hours' },
		{ value: '7d', label: '7 days' },
		{ value: '30d', label: '30 days' },
		{ value: '90d', label: '90 days' },
		{ value: '400d', label: '400 days' }
	];

	// Category chips shown before the catalog loads; the server's list
	// replaces them.
	let categories = $state<AuditCategory[]>([
		{ id: 'logins', label: 'Logins' },
		{ id: 'policy', label: 'Policy file' },
		{ id: 'devices', label: 'Devices' },
		{ id: 'users', label: 'Users' },
		{ id: 'keys', label: 'Keys' },
		{ id: 'settings', label: 'Settings' }
	]);

	const ROW = 44;
	const PAGE = 200;

	let status = $state<AuditStatus | null>(null);
	let statusError = $state(false);
	let events = $state<AuditEvent[]>([]);
	let facets = $state<AuditFacets | null>(null);
	let nextCursor = $state<string | undefined>(undefined);
	let loading = $state(false);
	let loadingMore = $state(false);
	let error = $state<string | null>(null);
	let selected = $state<AuditEvent | null>(null);
	let showFacets = $state(false);
	let showSource = $state(false);
	let showExport = $state(false);
	let view = $state<'events' | 'overview'>('events');
	let insights = $state<AuditInsights | null>(null);
	let nodeSummary = $state<AuditNodeSummary | null>(null);
	let catalogReady = $state(0);

	// Filters, mirrored into the URL so a view can be shared.
	let q = $state('');
	let actor = $state('');
	let target = $state('');
	let since = $state('30d');
	let eventFilter = $state<string[]>([]);
	let targetTypes = $state<string[]>([]);
	let targetId = $state('');
	let failed = $state(false);
	let categoryFilter = $state<string[]>([]);
	let actorTypes = $state<string[]>([]);
	let origins = $state<string[]>([]);
	let ephemeral = $state(false);
	// A window picked on the histogram overrides the range menu.
	let zoom = $state<{ start: string; end: string } | null>(null);

	let scrollTop = $state(0);
	let viewport = $state(0);
	let loadToken = 0;

	function readUrl() {
		const params = page.url.searchParams;
		q = params.get('q') ?? '';
		actor = params.get('actor') ?? '';
		target = params.get('target') ?? '';
		since = params.get('since') ?? '30d';
		eventFilter = params.getAll('event');
		targetTypes = params.getAll('targetType');
		targetId = params.get('targetId') ?? '';
		failed = params.get('failed') === 'true';
		categoryFilter = params.getAll('category');
		actorTypes = params.getAll('actorType');
		origins = params.getAll('origin');
		ephemeral = params.get('ephemeral') === 'true';
		view = params.get('view') === 'overview' ? 'overview' : 'events';
		const start = params.get('start');
		const end = params.get('end');
		zoom = start && end ? { start, end } : null;
	}

	function writeUrl() {
		const params = new URLSearchParams();
		const tailnet = page.url.searchParams.get('tailnet');
		if (tailnet) params.set('tailnet', tailnet);
		if (q.trim()) params.set('q', q.trim());
		if (actor.trim()) params.set('actor', actor.trim());
		if (target.trim()) params.set('target', target.trim());
		if (since !== '30d') params.set('since', since);
		for (const e of eventFilter) params.append('event', e);
		for (const t of targetTypes) params.append('targetType', t);
		if (targetId) params.set('targetId', targetId);
		if (failed) params.set('failed', 'true');
		for (const c of categoryFilter) params.append('category', c);
		for (const a of actorTypes) params.append('actorType', a);
		for (const o of origins) params.append('origin', o);
		if (ephemeral) params.set('ephemeral', 'true');
		if (zoom) {
			params.set('start', zoom.start);
			params.set('end', zoom.end);
		}
		if (view === 'overview') params.set('view', 'overview');
		if (selected) params.set('open', String(selected.id));
		const search = params.toString();
		void goto(`/audit${search ? `?${search}` : ''}`, { replace: true, shallow: true });
	}

	const query = $derived<AuditQuery>({
		since,
		start: zoom?.start,
		end: zoom?.end,
		q,
		actor: actor.trim() ? [actor.includes('@') ? actor.trim() : `~${actor.trim()}`] : [],
		target: target.trim() ? [target.trim()] : [],
		targetId: targetId ? [targetId] : [],
		event: eventFilter,
		targetType: targetTypes,
		category: categoryFilter,
		actorType: actorTypes,
		origin: origins,
		ephemeral,
		failed
	});

	const activeStatus = $derived<AuditTailnetStatus | null>(
		status?.tailnets.find((t) => t.id === $selectedTailnetId) ?? status?.tailnets[0] ?? null
	);
	const filtered = $derived(
		!!(q || actor || target || eventFilter.length || targetTypes.length || targetId || failed || categoryFilter.length || actorTypes.length || origins.length || ephemeral || zoom)
	);
	const targetName = $derived(nodeSummary?.name || facets?.targets.find((t) => t.value === targetId)?.label || targetId);
	const categoryCounts = $derived(Object.fromEntries((facets?.categories ?? []).map((c) => [c.value, c.count])));

	async function loadStatus() {
		try {
			status = await auditService.status();
			statusError = false;
		} catch {
			statusError = true;
		}
	}

	async function load(reset = true) {
		const token = reset ? ++loadToken : loadToken;
		if (reset) {
			loading = true;
			error = null;
		} else {
			loadingMore = true;
		}
		try {
			const pageQuery = { ...query, limit: PAGE, cursor: reset ? undefined : nextCursor };
			const [res, facetRes, insightRes, nodeRes] = await Promise.all([
				auditService.events(pageQuery),
				reset ? auditService.facets(query).catch(() => null) : Promise.resolve(facets),
				reset ? auditService.insights(query).catch(() => null) : Promise.resolve(insights),
				reset && targetId
					? auditService
							.node(targetId, { limit: 1 })
							.then((r) => r.summary)
							.catch(() => null)
					: Promise.resolve(reset ? null : nodeSummary)
			]);
			if (token !== loadToken) return;
			events = reset ? res.events : [...events, ...res.events];
			nextCursor = res.nextCursor;
			facets = facetRes;
			insights = insightRes;
			nodeSummary = nodeRes;
			if (reset) scrollTop = 0;
		} catch (err) {
			if (token !== loadToken) return;
			if (err instanceof APIError && (err.status === 403 || err.status === 404)) {
				await loadStatus();
			}
			error = err instanceof Error ? err.message : 'Failed to load audit logs';
		} finally {
			if (token === loadToken) {
				loading = false;
				loadingMore = false;
			}
		}
	}

	onMount(() => {
		readUrl();
		void loadStatus();
		void loadCatalog().then((catalog) => {
			if (!catalog) return;
			if (catalog.categories?.length) categories = catalog.categories;
			catalogReady++;
		});
	});

	// Reload when a filter or the tailnet changes. Text inputs wait a moment.
	let debounce: ReturnType<typeof setTimeout> | undefined;
	let lastKey = '';
	$effect(() => {
		const key = JSON.stringify([query, $selectedTailnetId, view]);
		if (!status?.enabled || !status.permitted) return;
		if (key === lastKey) return;
		lastKey = key;
		clearTimeout(debounce);
		debounce = setTimeout(() => {
			untrack(() => {
				writeUrl();
				void load(true);
			});
		}, 250);
	});

	// Open the event named in the URL once, e.g. from a shared link.
	let openedFromUrl = false;
	$effect(() => {
		if (openedFromUrl || !status?.permitted) return;
		const raw = page.url.searchParams.get('open');
		openedFromUrl = true;
		const id = Number(raw);
		if (!raw || !Number.isFinite(id)) return;
		auditService
			.event(id)
			.then((res) => (selected = res.event))
			.catch(() => {});
	});

	function select(event: AuditEvent | null) {
		selected = event;
		writeUrl();
	}

	async function openEvent(id: number) {
		try {
			const res = await auditService.event(id);
			select(res.event);
		} catch {
			/* the event may have aged out */
		}
	}

	async function download(format: 'csv' | 'ndjson') {
		showExport = false;
		const url = await auditService.exportUrl(query, format);
		const a = document.createElement('a');
		a.href = url;
		a.rel = 'noopener';
		document.body.appendChild(a);
		a.click();
		a.remove();
	}

	function toggleValue(list: string[], value: string): string[] {
		return list.includes(value) ? list.filter((v) => v !== value) : [...list, value];
	}

	function clearFilters() {
		q = '';
		actor = '';
		target = '';
		eventFilter = [];
		targetTypes = [];
		targetId = '';
		failed = false;
		categoryFilter = [];
		actorTypes = [];
		origins = [];
		ephemeral = false;
		zoom = null;
	}

	// Only the rows in view are rendered, so tens of thousands stay smooth.
	const first = $derived(Math.max(0, Math.floor(scrollTop / ROW) - 10));
	const last = $derived(Math.min(events.length, Math.ceil((scrollTop + viewport) / ROW) + 10));
	const visible = $derived(events.slice(first, last));

	function onScroll(e: Event) {
		const el = e.currentTarget as HTMLElement;
		scrollTop = el.scrollTop;
		if (nextCursor && !loadingMore && !loading && el.scrollTop + el.clientHeight > el.scrollHeight - ROW * 15) {
			void load(false);
		}
	}

	function onKey(e: KeyboardEvent) {
		if (e.key === 'Escape' && selected) select(null);
	}
</script>

<svelte:window onkeydown={onKey} />

<div class="flex h-dvh flex-col bg-background">
	<Header />
	{#if statusError}
		<AuditUnavailable reason="error" />
	{:else if !status}
		<div class="flex justify-center py-16 text-muted-foreground"><Loader2 class="h-5 w-5 animate-spin" /></div>
	{:else if !status.enabled}
		<AuditUnavailable reason="disabled" />
	{:else if !status.permitted}
		<AuditUnavailable reason="forbidden" />
	{:else}
		{#key catalogReady}
		<main class="flex min-h-0 flex-1 flex-col">
			<div class="border-b border-border px-3 py-3 sm:px-6">
				<div class="mb-2 flex flex-wrap items-center justify-between gap-2">
					<div>
						<h2 class="text-base font-semibold">Audit log</h2>
						<p class="text-xs text-muted-foreground">Who changed what in the tailnet, from Tailscale's configuration audit log.</p>
					</div>
					{#if activeStatus}
						<div class="relative flex items-center gap-2 text-xs text-muted-foreground" data-testid="audit-source-status">
							<button class="flex items-center gap-2 rounded-md px-1 py-0.5 hover:bg-secondary" onclick={() => (showSource = !showSource)} title="Audit source status">
							{#if activeStatus.source.lastError}
								<span class="inline-flex items-center gap-1 text-amber-700 dark:text-amber-400" title={activeStatus.source.lastError}>
									<AlertTriangle class="h-3.5 w-3.5" /> Last poll failed
								</span>
							{:else if activeStatus.source.backend !== 'off'}
								<span class="h-2 w-2 rounded-full {activeStatus.source.running ? 'bg-green-500' : 'bg-muted-foreground/40'}"></span>
							{/if}
							<span>
								{activeStatus.stored.events.toLocaleString()} events stored ·
								{activeStatus.source.backend === 'off' ? 'no source' : `from ${activeStatus.source.backend}`}
								{#if activeStatus.stored.lastIngest || activeStatus.source.lastSuccess} · updated {relativeTime(activeStatus.stored.lastIngest || activeStatus.source.lastSuccess)}{/if}
								{#if status.retention} · kept {status.retention}{/if}
							</span>
							</button>
							{#if showSource}
								<div class="absolute right-0 top-full z-30 mt-1 w-[26rem] max-w-[90vw] shadow-lg">
									<AuditSourceCard status={activeStatus} retention={status.retention} />
								</div>
							{/if}
							<button class="rounded-md p-1 hover:bg-secondary" title="Refresh" onclick={() => { void loadStatus(); void load(true); }}>
								<RefreshCw class="h-3.5 w-3.5 {loading ? 'animate-spin' : ''}" />
							</button>
						</div>
					{/if}
				</div>
				<div class="flex flex-wrap items-center gap-2">
					<label class="relative min-w-48 flex-1">
						<Search class="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
						<input
							class="h-8 w-full rounded-md border border-border bg-background pl-7 pr-2 text-sm"
							placeholder="Search actor, target, event, details"
							bind:value={q}
							aria-label="Search audit log"
						/>
					</label>
					<input class="h-8 w-44 rounded-md border border-border bg-background px-2 text-sm" placeholder="Who (login or name)" bind:value={actor} aria-label="Actor" />
					<input class="h-8 w-44 rounded-md border border-border bg-background px-2 text-sm" placeholder="What (device, user, key)" bind:value={target} aria-label="Target" />
					<select class="h-8 rounded-md border border-border bg-background px-2 text-sm" bind:value={since} onchange={() => (zoom = null)} aria-label="Time range">
						{#each RANGES as range}<option value={range.value}>Last {range.label}</option>{/each}
					</select>
					<div class="inline-flex h-8 overflow-hidden rounded-md border border-border text-sm" role="tablist" aria-label="View">
						<button role="tab" aria-selected={view === 'events'} class="px-2.5 {view === 'events' ? 'bg-secondary font-medium' : 'hover:bg-secondary/60'}" onclick={() => (view = 'events')}>Events</button>
						<button role="tab" aria-selected={view === 'overview'} class="border-l border-border px-2.5 {view === 'overview' ? 'bg-secondary font-medium' : 'hover:bg-secondary/60'}" onclick={() => (view = 'overview')}>Overview</button>
					</div>
					<div class="relative">
						<button class="inline-flex h-8 items-center gap-1 rounded-md border border-border px-2 text-sm hover:bg-secondary" onclick={() => (showExport = !showExport)} aria-haspopup="menu" title="Download the events that match these filters">
							<Download class="h-3.5 w-3.5" /> Export
						</button>
						{#if showExport}
							<div class="absolute right-0 top-full z-30 mt-1 w-56 rounded-md border border-border bg-card p-1 text-sm shadow-lg" role="menu" data-testid="audit-export-menu">
								<button class="block w-full rounded px-2 py-1.5 text-left hover:bg-secondary" role="menuitem" onclick={() => download('csv')}>
									CSV <span class="block text-[11px] text-muted-foreground">One row per event, for spreadsheets</span>
								</button>
								<button class="block w-full rounded px-2 py-1.5 text-left hover:bg-secondary" role="menuitem" onclick={() => download('ndjson')}>
									NDJSON <span class="block text-[11px] text-muted-foreground">Original Tailscale records, one per line</span>
								</button>
								<p class="px-2 pb-1 pt-1.5 text-[11px] text-muted-foreground">{(facets?.total ?? 0).toLocaleString()} events · current filters</p>
							</div>
						{/if}
					</div>
					<button
						class="inline-flex h-8 items-center gap-1 rounded-md border border-border px-2 text-sm hover:bg-secondary lg:hidden"
						onclick={() => (showFacets = !showFacets)}
					>
						<Filter class="h-3.5 w-3.5" /> Filters
					</button>
				</div>
				<div class="mt-2 flex flex-wrap items-center gap-1.5 text-xs">
					{#each categories as cat (cat.id)}
						{@const on = categoryFilter.includes(cat.id)}
						{@const n = categoryCounts[cat.id] ?? 0}
						{#if on || n > 0 || !facets}
							<button
								class="rounded-full border px-2.5 py-0.5 {on ? 'border-primary bg-primary/10 text-primary' : 'border-border hover:bg-secondary'}"
								onclick={() => (categoryFilter = toggleValue(categoryFilter, cat.id))}
							>
								{cat.label}{#if facets}<span class="ml-1 tabular-nums text-muted-foreground">{n.toLocaleString()}</span>{/if}
							</button>
						{/if}
					{/each}
					<button
						class="rounded-full border px-2.5 py-0.5 {failed ? 'border-red-500 bg-red-500/10 text-red-700 dark:text-red-300' : 'border-border hover:bg-secondary'}"
						onclick={() => (failed = !failed)}
						title="Changes that failed, including requests Tailscale refused"
					>
						Failed{#if facets}<span class="ml-1 tabular-nums text-muted-foreground">{facets.failed.toLocaleString()}</span>{/if}
					</button>
					<button
						class="rounded-full border px-2.5 py-0.5 {ephemeral ? 'border-violet-500 bg-violet-500/10 text-violet-700 dark:text-violet-300' : 'border-border hover:bg-secondary'}"
						onclick={() => (ephemeral = !ephemeral)}
						title="Events about ephemeral devices"
					>
						Ephemeral devices{#if facets}<span class="ml-1 tabular-nums text-muted-foreground">{facets.ephemeral.toLocaleString()}</span>{/if}
					</button>
					{#if targetId}
						<span class="inline-flex items-center gap-1 rounded-full border border-primary bg-primary/10 px-2.5 py-0.5 text-primary">
							{targetName}
							<button aria-label="Clear target" onclick={() => (targetId = '')}><X class="h-3 w-3" /></button>
						</span>
					{/if}
					{#if zoom}
						<span class="inline-flex items-center gap-1 rounded-full border border-primary bg-primary/10 px-2.5 py-0.5 text-primary">
							{auditTime(zoom.start)} – {auditTime(zoom.end)}
							<button aria-label="Clear time window" onclick={() => (zoom = null)}><X class="h-3 w-3" /></button>
						</span>
					{/if}
					{#if filtered}
						<button class="ml-1 text-muted-foreground underline-offset-2 hover:underline" onclick={clearFilters}>Clear filters</button>
					{/if}
				</div>
			</div>
			{#if insights && insights.histogram.buckets.length > 0}
				<div class="border-b border-border">
					<AuditHistogram histogram={insights.histogram} active={zoom} onpick={(start, end) => (zoom = { start, end })} />
				</div>
			{/if}
			{#if targetId && nodeSummary}
				<AuditDeviceSummary summary={nodeSummary} onopen={openEvent} onclose={() => (targetId = '')} />
			{/if}

			<div class="flex min-h-0 flex-1">
				<aside class="{showFacets ? 'block' : 'hidden'} w-60 shrink-0 overflow-y-auto border-r border-border p-3 text-xs lg:block">
					{#if facets}
						{#snippet facetList(title: string, items: AuditFacets['events'], active: string[], onpick: (v: string) => void, label: (v: string) => string)}
							<h3 class="mb-1 mt-3 font-medium text-muted-foreground first:mt-0">{title}</h3>
							{#if items.length === 0}
								<p class="text-muted-foreground">None</p>
							{/if}
							<ul>
								{#each items.slice(0, 12) as item}
									<li>
										<button
											class="flex w-full items-center justify-between gap-2 rounded px-1.5 py-1 text-left hover:bg-secondary/60 {active.includes(item.value) ? 'bg-primary/10 text-primary' : ''}"
											onclick={() => onpick(item.value)}
											title={item.value}
										>
											<span class="truncate">{label(item.value)}</span>
											<span class="shrink-0 tabular-nums text-muted-foreground">{item.count.toLocaleString()}</span>
										</button>
									</li>
								{/each}
							</ul>
						{/snippet}
						<div class="mb-2 text-muted-foreground"><span class="font-semibold text-foreground tabular-nums">{facets.total.toLocaleString()}</span> events match</div>
						{@render facetList('Category', facets.categories, categoryFilter, (v) => (categoryFilter = toggleValue(categoryFilter, v)), categoryLabel)}
						{@render facetList('Event', facets.events, eventFilter, (v) => (eventFilter = toggleValue(eventFilter, v)), eventLabel)}
						{@render facetList('Who', facets.actors, actor ? [actor] : [], (v) => (actor = actor === v ? '' : v), (v) => (v.includes('@') ? v : facets?.actors.find((x) => x.value === v)?.label || actorTypeLabel(v)))}
						{@render facetList('Kind of actor', facets.actorTypes, actorTypes, (v) => (actorTypes = toggleValue(actorTypes, v)), actorTypeLabel)}
						{@render facetList('What changed', facets.targets, targetId ? [targetId] : [], (v) => (targetId = targetId === v ? '' : v), (v) => facets?.targets.find((t) => t.value === v)?.label || v)}
						{@render facetList('Target type', facets.targetTypes, targetTypes, (v) => (targetTypes = toggleValue(targetTypes, v)), (v) => v.toLowerCase().replace(/_/g, ' '))}
						{@render facetList('Made via', facets.origins, origins, (v) => (origins = toggleValue(origins, v)), originLabel)}
					{/if}
				</aside>

				{#if view === 'overview'}
					{#if insights}
						<AuditOverview {insights} {facets} onopen={openEvent} onactor={(v) => (actor = v)} ontarget={(v) => (targetId = v)} />
					{:else}
						<div class="flex flex-1 justify-center py-16 text-muted-foreground"><Loader2 class="h-5 w-5 animate-spin" /></div>
					{/if}
				{:else}
				<section class="flex min-w-0 flex-1 flex-col">
					<div class="grid grid-cols-[10rem_12rem_minmax(0,1fr)_minmax(0,1fr)] gap-3 border-b border-border bg-secondary/30 px-3 py-1.5 text-[11px] font-medium uppercase tracking-wide text-muted-foreground max-md:grid-cols-[7rem_minmax(0,1fr)_minmax(0,1fr)] sm:px-4" role="row">
						<span>Time</span>
						<span class="max-md:hidden">Event</span>
						<span>Who</span>
						<span>What</span>
					</div>
					{#if loading && events.length === 0}
						<div class="flex justify-center py-16 text-muted-foreground"><Loader2 class="h-5 w-5 animate-spin" /></div>
					{:else if error}
						<p class="py-8 text-center text-sm text-destructive">{error}</p>
					{:else if events.length === 0}
						<div class="mx-auto mt-10 w-full max-w-xl px-3 text-center text-sm text-muted-foreground" data-testid="audit-empty">
							{#if filtered}
								<p class="mb-2 font-medium text-foreground">No events match these filters.</p>
								<button class="underline underline-offset-2" onclick={clearFilters}>Clear filters</button>
							{:else if activeStatus && activeStatus.stored.events === 0}
								<p class="mb-2 font-medium text-foreground">No audit events stored yet.</p>
								{#if activeStatus.source.backend === 'off'}
									<p>This tailnet has no audit source. Add an <code class="font-mono text-xs">audit:</code> block for it in the tailnets file.</p>
								{:else}
									<p class="mb-3">
										{activeStatus.source.lastError ? 'tsflow could not read the audit source yet.' : `tsflow is reading from ${activeStatus.source.backend}. Events show up after the next poll.`}
									</p>
									<AuditSourceCard status={activeStatus} retention={status.retention} />
								{/if}
							{:else}
								<p>No events in the last {RANGES.find((r) => r.value === since)?.label ?? since}.</p>
							{/if}
						</div>
					{:else}
						<div class="min-h-0 flex-1 overflow-y-auto" onscroll={onScroll} bind:clientHeight={viewport} role="table" aria-label="Audit events" data-testid="audit-table">
							<div style="height: {events.length * ROW}px; position: relative;">
								<div style="transform: translateY({first * ROW}px)">
									{#each visible as event (event.id)}
										{@const tone = eventTone(event)}
										<button
											class="grid w-full grid-cols-[10rem_12rem_minmax(0,1fr)_minmax(0,1fr)] items-center gap-3 border-b border-border/50 px-3 text-left text-sm hover:bg-secondary/40 max-md:grid-cols-[7rem_minmax(0,1fr)_minmax(0,1fr)] sm:px-4 {selected?.id === event.id ? 'bg-primary/5' : ''}"
											style="height: {ROW}px"
											role="row"
											onclick={() => select(event)}
										>
											<span class="truncate text-xs tabular-nums text-muted-foreground" title={auditTime(event.eventTime)}>
												{new Date(event.eventTime).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
											</span>
											<span class="truncate max-md:hidden">
												<span class="rounded px-1.5 py-0.5 text-xs font-medium {TONE_CLASSES[tone]}" title={event.event}>{eventLabel(event.event)}</span>
											</span>
											<span class="flex min-w-0 items-center gap-1.5">
												<span class="truncate">{actorLabel(event)}</span>
												{#if event.actor.type && event.actor.type !== 'USER' && !actorLabel(event).startsWith(actorTypeLabel(event.actor.type))}
													<span class="shrink-0 rounded bg-secondary px-1 text-[10px] text-muted-foreground">{actorTypeLabel(event.actor.type)}</span>
												{/if}
											</span>
											<span class="flex min-w-0 items-center gap-1.5 text-muted-foreground">
												<span class="truncate"><span class="md:hidden">{eventLabel(event.event)} · </span>{targetLabel(event, true)}</span>
												{#if event.target.isEphemeral}
													<span class="shrink-0 rounded bg-violet-500/15 px-1 text-[10px] text-violet-700 dark:text-violet-300">ephemeral</span>
												{/if}
												{#if event.deferredAt}
													<span class="shrink-0 rounded bg-amber-500/15 px-1 text-[10px] text-amber-800 dark:text-amber-300" title="Logged late by Tailscale's rate limiter">delayed</span>
												{/if}
											</span>
										</button>
									{/each}
								</div>
							</div>
							{#if loadingMore}
								<div class="flex justify-center py-3 text-muted-foreground"><Loader2 class="h-4 w-4 animate-spin" /></div>
							{:else if !nextCursor}
								<p class="py-3 text-center text-xs text-muted-foreground">{events.length.toLocaleString()} events · end of results</p>
							{/if}
						</div>
					{/if}
				</section>
				{/if}

				{#if selected}
					<aside class="fixed inset-0 z-40 overflow-y-auto bg-card p-4 sm:static sm:z-auto sm:w-[30rem] sm:shrink-0 sm:border-l sm:border-border" aria-label="Event detail" data-testid="audit-detail">
						<div class="mb-3 flex items-center justify-between">
							<h2 class="text-sm font-semibold">Event detail</h2>
							<button class="rounded-md p-1 hover:bg-secondary" aria-label="Close" onclick={() => select(null)}><X class="h-4 w-4" /></button>
						</div>
						<AuditEventDetail event={selected} onselect={(e) => select(e)} />
						<p class="mt-4 text-[11px] text-muted-foreground">
							<a class="underline underline-offset-2" href={hrefWithTailnet(`/audit?open=${selected.id}`, $selectedTailnetId)}>Link to this event</a>
						</p>
					</aside>
				{/if}
			</div>
		</main>
		{/key}
	{/if}
</div>
