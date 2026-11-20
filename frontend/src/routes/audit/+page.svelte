<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { AlertTriangle, Filter, Loader2, RefreshCw, Search, X } from 'lucide-svelte';
	import Header from '#lib/components/layout/Header.svelte';
	import AuditEventDetail from '#lib/components/audit/AuditEventDetail.svelte';
	import AuditUnavailable from '#lib/components/audit/AuditUnavailable.svelte';
	import AuditSourceCard from '#lib/components/audit/AuditSourceCard.svelte';
	import { auditService } from '#lib/audit/audit-service';
	import { actorLabel, auditTime, eventLabel, eventTone, relativeTime, targetLabel, TONE_CLASSES } from '#lib/audit/format';
	import type { AuditEvent, AuditFacets, AuditQuery, AuditStatus, AuditTailnetStatus } from '#lib/audit/types';
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

	// Shortcuts for what people look for most often.
	const PRESETS = [
		{ label: 'Logins', events: ['NODE.LOGIN', 'ADMIN_CONSOLE.LOGIN', 'WEB_INTERFACE.LOGIN'] },
		{ label: 'Policy file', events: ['TAILNET.UPDATE.ACL', 'TAILNET.UPDATE.POLICY'] },
		{ label: 'Devices', events: ['NODE.*'] },
		{ label: 'Users', events: ['USER.*'] },
		{ label: 'Keys', events: ['API_KEY.*', 'AUTH_KEY.*', 'OAUTH_CLIENT.*'] }
	];

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

	// Filters, mirrored into the URL so a view can be shared.
	let q = $state('');
	let actor = $state('');
	let target = $state('');
	let since = $state('30d');
	let eventFilter = $state<string[]>([]);
	let targetTypes = $state<string[]>([]);
	let targetId = $state('');
	let failed = $state(false);

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
		if (selected) params.set('open', String(selected.id));
		const search = params.toString();
		void goto(`/audit${search ? `?${search}` : ''}`, { replace: true, shallow: true });
	}

	const query = $derived<AuditQuery>({
		since,
		q,
		actor: actor.trim() ? [actor.includes('@') ? actor.trim() : `~${actor.trim()}`] : [],
		target: target.trim() ? [target.trim()] : [],
		targetId: targetId ? [targetId] : [],
		event: eventFilter,
		targetType: targetTypes,
		failed
	});

	const activeStatus = $derived<AuditTailnetStatus | null>(
		status?.tailnets.find((t) => t.id === $selectedTailnetId) ?? status?.tailnets[0] ?? null
	);
	const filtered = $derived(!!(q || actor || target || eventFilter.length || targetTypes.length || targetId || failed));

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
			const [res, facetRes] = await Promise.all([
				auditService.events(pageQuery),
				reset ? auditService.facets(query).catch(() => null) : Promise.resolve(facets)
			]);
			if (token !== loadToken) return;
			events = reset ? res.events : [...events, ...res.events];
			nextCursor = res.nextCursor;
			facets = facetRes;
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
	});

	// Reload when a filter or the tailnet changes. Text inputs wait a moment.
	let debounce: ReturnType<typeof setTimeout> | undefined;
	let lastKey = '';
	$effect(() => {
		const key = JSON.stringify([query, $selectedTailnetId]);
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

	function togglePreset(preset: (typeof PRESETS)[number]) {
		const on = preset.events.every((e) => eventFilter.includes(e));
		eventFilter = on ? eventFilter.filter((e) => !preset.events.includes(e)) : [...new Set([...eventFilter, ...preset.events])];
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
					<select class="h-8 rounded-md border border-border bg-background px-2 text-sm" bind:value={since} aria-label="Time range">
						{#each RANGES as range}<option value={range.value}>Last {range.label}</option>{/each}
					</select>
					<button
						class="inline-flex h-8 items-center gap-1 rounded-md border border-border px-2 text-sm hover:bg-secondary lg:hidden"
						onclick={() => (showFacets = !showFacets)}
					>
						<Filter class="h-3.5 w-3.5" /> Filters
					</button>
				</div>
				<div class="mt-2 flex flex-wrap items-center gap-1.5 text-xs">
					{#each PRESETS as preset}
						{@const on = preset.events.every((e) => eventFilter.includes(e))}
						<button
							class="rounded-full border px-2.5 py-0.5 {on ? 'border-primary bg-primary/10 text-primary' : 'border-border hover:bg-secondary'}"
							onclick={() => togglePreset(preset)}
						>
							{preset.label}
						</button>
					{/each}
					<button
						class="rounded-full border px-2.5 py-0.5 {failed ? 'border-red-500 bg-red-500/10 text-red-700 dark:text-red-300' : 'border-border hover:bg-secondary'}"
						onclick={() => (failed = !failed)}
					>
						Failed only
					</button>
					{#if targetId}
						<span class="inline-flex items-center gap-1 rounded-full border border-primary bg-primary/10 px-2.5 py-0.5 text-primary">
							Device {targetId}
							<button aria-label="Clear device" onclick={() => (targetId = '')}><X class="h-3 w-3" /></button>
						</span>
					{/if}
					{#if filtered}
						<button class="ml-1 text-muted-foreground underline-offset-2 hover:underline" onclick={clearFilters}>Clear filters</button>
					{/if}
				</div>
			</div>

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
						{@render facetList('Event', facets.events, eventFilter, (v) => (eventFilter = toggleValue(eventFilter, v)), eventLabel)}
						{@render facetList('Who', facets.actors, actor ? [actor] : [], (v) => (actor = actor === v ? '' : v), (v) => v)}
						{@render facetList('Target type', facets.targetTypes, targetTypes, (v) => (targetTypes = toggleValue(targetTypes, v)), (v) => v.toLowerCase().replace(/_/g, ' '))}
					{/if}
				</aside>

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
											<span class="truncate">{actorLabel(event)}</span>
											<span class="truncate text-muted-foreground">
												<span class="md:hidden">{eventLabel(event.event)} · </span>{targetLabel(event, true)}
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
	{/if}
</div>
