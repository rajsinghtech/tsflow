<script lang="ts">
	import { CalendarX } from 'lucide-svelte';
	import { dataSourceStore } from '#lib/stores/data-source-store';
	import { commitIntent } from '#lib/stores/time-range-history';
	import { storedCoverage, windowCoverage } from '#lib/stores/traffic-shape';
	import { canJumpToLatest, emptyRangeReason, jumpWindowMs, storedRangeLabel } from './range-coverage';
	import { loadTimeZone } from './time-range-url';

	let { compact = false }: { compact?: boolean } = $props();

	const zone = loadTimeZone();
	const stored = $derived(storedRangeLabel($storedCoverage, zone));
	const reason = $derived(emptyRangeReason($windowCoverage));
	const showJump = $derived(canJumpToLatest($windowCoverage, $dataSourceStore.followLatest, $storedCoverage));

	function jumpToLatest() {
		const state = $dataSourceStore;
		const selectedMs =
			state.selectedStart && state.selectedEnd ? state.selectedEnd.getTime() - state.selectedStart.getTime() : null;
		commitIntent({ kind: 'sliding', windowMs: jumpWindowMs(selectedMs, $storedCoverage) }, zone);
	}
</script>

<div
	class="flex flex-col items-center justify-center gap-2 text-center {compact ? 'px-4 py-4' : 'flex-1 p-6'}"
	role="status"
	data-testid="empty-range"
>
	{#if !compact}
		<CalendarX class="h-8 w-8 text-muted-foreground" aria-hidden="true" />
	{/if}
	<p class="font-medium">No data in this range</p>
	<p class="max-w-md text-sm text-muted-foreground">{reason}</p>
	{#if stored}
		<p class="text-xs text-muted-foreground">Stored data: {stored}</p>
	{/if}
	{#if showJump}
		<button
			type="button"
			class="mt-1 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)]"
			onclick={jumpToLatest}
		>
			Jump to latest data
		</button>
	{/if}
</div>
