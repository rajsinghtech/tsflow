<script lang="ts">
	import { CalendarClock, ChevronDown, ChevronLeft, ChevronRight, ChevronUp, Copy, Minus, RefreshCw } from 'lucide-svelte';
	import { onMount } from 'svelte';
	import { get } from 'svelte/store';
	import { page } from '$app/state';
	import { dataSourceStore } from '#lib/stores/data-source-store';
	import { refreshVisibleData } from '#lib/stores/live-mode';
	import {
		bootstrapRangeFromLocation,
		commitIntent,
		copyRangeLink,
		pinCurrentRange,
		resumeSlidingLive,
		shiftWindow,
		syncRangeFromUrl,
		zoomWindow
	} from '#lib/stores/time-range-history';
	import { nextPollAt, pollerIsBehind, refreshIntervalMs } from '#lib/utils/poll-interval';
	import { formatDate } from '#lib/utils/format';
	import TimelineStrip from './TimelineStrip.svelte';
	import TimeRangePicker from './TimeRangePicker.svelte';
	import {
		formatClock,
		intentFromParts,
		intentLabel,
		loadRecent,
		loadTimeZone,
		loadTimelineCollapsed,
		saveTimeZone,
		saveTimelineCollapsed,
		type RangeIntent,
		type TimeZoneMode
	} from './time-range-url';

	bootstrapRangeFromLocation();

	onMount(() => {
		void dataSourceStore.fetchDataRange();
		void dataSourceStore.fetchPollerStatus();
		const timer = setInterval(() => {
			void dataSourceStore.fetchPollerStatus();
			if (get(dataSourceStore).followLatest) void dataSourceStore.fetchDataRange();
		}, 30_000);
		return () => clearInterval(timer);
	});

	let open = $state(false);
	let collapsed = $state(loadTimelineCollapsed());
	let zone = $state<TimeZoneMode>(loadTimeZone());
	let recent = $state(loadRecent());
	let refreshing = $state(false);
	let copied = $state(false);
	let tick = $state(0);
	let chord = false;
	let chordTimer: ReturnType<typeof setTimeout> | null = null;
	let pickerRoot: HTMLDivElement | null = $state(null);

	const source = $derived($dataSourceStore);
	const intent = $derived(
		intentFromParts({
			live: source.followLatest,
			windowMs: source.latestWindowMs,
			anchoredStart: source.anchoredStart,
			start: source.selectedStart,
			end: source.selectedEnd
		})
	);
	const live = $derived(intent.kind !== 'absolute');
	const pillLabel = $derived(live ? `Live · ${intentLabel(intent, zone)}` : intentLabel(intent, zone));
	const atCoverageEnd = $derived.by(() => {
		if (!source.dataRange || !source.selectedEnd) return false;
		return source.selectedEnd.getTime() >= new Date(source.dataRange.latest).getTime() - 1000;
	});

	const freshness = $derived.by(() => {
		void tick;
		const latest = source.dataRange?.latest ? new Date(source.dataRange.latest) : null;
		const poll = source.pollerStatus;
		const interval = refreshIntervalMs(poll?.pollInterval);
		const lastMs = poll?.lastPollTime ? new Date(poll.lastPollTime).getTime() : 0;
		const now = Date.now();
		const behind = lastMs > 0 && new Date(poll?.lastPollTime ?? 0).getFullYear() > 1970 && pollerIsBehind(lastMs, interval, now);
		return {
			through: latest ? formatClock(latest, zone) : '--',
			next: lastMs > 0 ? formatClock(new Date(nextPollAt(lastMs, interval, now)), zone) : '--',
			behind,
			records: (poll?.database?.dataRange?.count ?? poll?.database?.tableCounts?.flow_logs_current ?? 0).toLocaleString(),
			lastPoll: poll?.lastPollTime && new Date(poll.lastPollTime).getFullYear() > 1970 ? formatDate(poll.lastPollTime) : '—',
			interval: poll?.pollInterval || '—'
		};
	});

	$effect(() => {
		const timer = setInterval(() => tick++, 10_000);
		return () => clearInterval(timer);
	});

	$effect(() => {
		syncRangeFromUrl(page.url.search);
	});

	function toggleZone(next: TimeZoneMode) {
		zone = next;
		saveTimeZone(next);
	}

	function choose(next: RangeIntent) {
		open = false;
		recent = loadRecent();
		commitIntent(next, zone);
		recent = loadRecent();
	}

	function toggleCollapsed() {
		collapsed = !collapsed;
		saveTimelineCollapsed(collapsed);
	}

	async function refresh() {
		refreshing = true;
		try {
			await refreshVisibleData();
		} finally {
			refreshing = false;
		}
	}

	async function copyLink() {
		const ok = await copyRangeLink();
		copied = ok;
		if (ok) setTimeout(() => (copied = false), 1500);
	}

	function onKey(event: KeyboardEvent) {
		const target = event.target;
		if (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement) return;
		if (target instanceof HTMLElement && (target.closest('.cm-editor') || target.closest('#time-range-popover'))) return;
		if (event.metaKey || event.ctrlKey || event.altKey) return;
		if (event.key === 'Escape' && open) {
			open = false;
			return;
		}
		if (!chord && (event.key === 'p' || event.key === 'P')) {
			event.preventDefault();
			if (get(dataSourceStore).followLatest) pinCurrentRange();
			else resumeSlidingLive();
			return;
		}
		if (!chord && (event.key === 'r' || event.key === 'R')) {
			event.preventDefault();
			void refresh();
			return;
		}
		if (!chord && (event.key === 't' || event.key === 'T')) {
			chord = true;
			if (chordTimer) clearTimeout(chordTimer);
			chordTimer = setTimeout(() => {
				chord = false;
			}, 1200);
			return;
		}
		if (!chord) return;
		chord = false;
		if (event.key === 'ArrowLeft') {
			event.preventDefault();
			shiftWindow(-1);
		} else if (event.key === 'ArrowRight') {
			event.preventDefault();
			shiftWindow(1);
		} else if (event.key === '-' || event.key === 'z' || event.key === 'Z') {
			event.preventDefault();
			zoomWindow(2);
		} else if (event.key === '+' || event.key === '=') {
			event.preventDefault();
			zoomWindow(0.5);
		} else if (event.key === 'c' || event.key === 'C') {
			event.preventDefault();
			void copyLink();
		}
	}

	function onWindowClick(event: MouseEvent) {
		if (open && pickerRoot && !pickerRoot.contains(event.target as Node)) open = false;
	}
</script>

<svelte:window onkeydown={onKey} onclick={onWindowClick} />

<div class="border-t border-border">
	<div class="flex items-center gap-1 px-2 py-1">
		<button
			type="button"
			class="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border hover:bg-secondary focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)]"
			aria-label="Shift backward"
			aria-keyshortcuts="t ArrowLeft"
			title="Shift backward (t then left arrow)"
			onclick={() => shiftWindow(-1)}
		>
			<ChevronLeft class="h-4 w-4" />
		</button>

		<div class="relative" bind:this={pickerRoot}>
			<button
				type="button"
				class="inline-flex h-8 max-w-[16rem] items-center gap-1.5 rounded-md border border-border bg-background px-2 text-xs hover:bg-secondary focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)]"
				aria-haspopup="dialog"
				aria-expanded={open}
				aria-controls="time-range-popover"
				aria-label={pillLabel}
				onclick={() => (open = !open)}
			>
				{#if live}
					<span class="live-pulse h-2 w-2 shrink-0 rounded-full bg-primary" aria-hidden="true"></span>
				{/if}
				<span class="hidden truncate sm:inline">{pillLabel}</span>
				<CalendarClock class="h-4 w-4 sm:hidden" />
				<ChevronDown class="hidden h-3.5 w-3.5 shrink-0 text-muted-foreground sm:inline" />
			</button>
			{#if open}
				<TimeRangePicker {zone} {recent} now={new Date()} onCommit={choose} onZone={toggleZone} />
			{/if}
		</div>

		<button
			type="button"
			class="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border hover:bg-secondary focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)]"
			aria-label="Zoom out"
			aria-keyshortcuts="t Minus"
			title="Zoom out (t then minus, or t then z)"
			onclick={() => zoomWindow(2)}
		>
			<Minus class="h-4 w-4" />
		</button>
		<button
			type="button"
			class="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border hover:bg-secondary focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)] disabled:cursor-not-allowed disabled:opacity-40"
			aria-label="Shift forward"
			aria-keyshortcuts="t ArrowRight"
			title="Shift forward (t then right arrow)"
			disabled={live || atCoverageEnd}
			onclick={() => shiftWindow(1)}
		>
			<ChevronRight class="h-4 w-4" />
		</button>

		{#if live}
			<button
				type="button"
				class="group relative ml-1 hidden min-w-0 truncate text-[11px] md:inline {freshness.behind ? 'text-amber-500' : 'text-muted-foreground'}"
			>
				data through {freshness.through} · next poll ≈{freshness.next}
				<span
					class="pointer-events-none absolute top-full left-0 z-40 mt-1 hidden w-56 rounded-md border border-border bg-popover p-2 text-left text-xs text-popover-foreground shadow-lg group-hover:block group-focus:block"
				>
					<div class="flex justify-between gap-3"><span>Stored records</span><span class="font-mono">{freshness.records}</span></div>
					<div class="mt-1 flex justify-between gap-3"><span>Last poll</span><span class="font-mono">{freshness.lastPoll}</span></div>
					<div class="mt-1 flex justify-between gap-3"><span>Poll interval</span><span class="font-mono">{freshness.interval}</span></div>
				</span>
			</button>
		{/if}

		<div class="ml-auto flex items-center gap-1">
			<button
				type="button"
				class="inline-flex h-8 w-8 items-center justify-center rounded-md border border-transparent text-muted-foreground hover:border-border hover:bg-secondary hover:text-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)]"
				aria-label="Refresh now"
				title="Refresh now (R)"
				onclick={() => void refresh()}
				disabled={refreshing}
			>
				<RefreshCw class="h-3.5 w-3.5 {refreshing ? 'animate-spin' : ''}" />
			</button>
			<button
				type="button"
				class="inline-flex h-8 w-8 items-center justify-center rounded-md border border-transparent text-muted-foreground hover:border-border hover:bg-secondary hover:text-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)]"
				aria-label={copied ? 'Link copied' : 'Copy link'}
				aria-keyshortcuts="t c"
				title="Copy link (t then c)"
				onclick={() => void copyLink()}
			>
				<Copy class="h-3.5 w-3.5" />
			</button>
			<button
				type="button"
				class="inline-flex h-8 w-8 items-center justify-center rounded-md border border-transparent text-muted-foreground hover:border-border hover:bg-secondary hover:text-foreground"
				aria-expanded={!collapsed}
				aria-label={collapsed ? 'Show timeline' : 'Hide timeline'}
				onclick={toggleCollapsed}
			>
				{#if collapsed}
					<ChevronDown class="h-4 w-4" />
				{:else}
					<ChevronUp class="h-4 w-4" />
				{/if}
			</button>
		</div>
	</div>
	{#if !collapsed}
		<TimelineStrip {zone} />
	{/if}
</div>
