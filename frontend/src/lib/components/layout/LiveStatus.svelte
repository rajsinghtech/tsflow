<script lang="ts">
	import { RefreshCw } from 'lucide-svelte';
	import { formatRelativeTime } from '#lib/utils/format';

	let {
		live,
		windowLabel = '',
		updatedAt = null,
		everyLabel = '',
		refreshing = false,
		canReturn = true,
		compact = false,
		onBackToLive,
		onRefresh
	}: {
		live: boolean;
		windowLabel?: string;
		updatedAt?: Date | null;
		everyLabel?: string;
		refreshing?: boolean;
		canReturn?: boolean;
		compact?: boolean;
		onBackToLive: () => void;
		onRefresh: () => void;
	} = $props();

	let tick = $state(0);
	$effect(() => {
		const interval = setInterval(() => tick++, 10_000);
		return () => clearInterval(interval);
	});

	const updatedLabel = $derived.by(() => {
		void tick;
		if (!updatedAt) return '';
		return `Updated ${formatRelativeTime(updatedAt)}`;
	});
</script>

<div class="flex items-center gap-1.5 {compact ? '' : 'justify-between'}">
	{#if live}
		<div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5" role="status">
			<span class="inline-flex items-center gap-1.5 text-xs font-medium text-foreground">
				<span class="live-pulse h-2 w-2 rounded-full bg-primary" aria-hidden="true"></span>
				Live{windowLabel ? ` · ${windowLabel}` : ''}
			</span>
			{#if updatedLabel || (!compact && everyLabel)}
				<span class="text-xs text-muted-foreground {compact ? 'hidden md:inline' : ''}">
					{updatedLabel}{!compact && everyLabel ? `${updatedLabel ? ' · ' : ''}every ${everyLabel}` : ''}
				</span>
			{/if}
		</div>
	{:else}
		<button
			type="button"
			onclick={onBackToLive}
			disabled={!canReturn}
			class="inline-flex min-h-8 items-center rounded-md bg-primary px-2.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)] disabled:cursor-not-allowed"
			title="Back to live (P)"
		>
			Back to live
		</button>
		{#if !compact && windowLabel}
			<span class="text-xs text-muted-foreground">Pinned · {windowLabel}</span>
		{/if}
	{/if}
	<button
		type="button"
		onclick={onRefresh}
		disabled={refreshing}
		class="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-transparent text-muted-foreground hover:border-border hover:bg-secondary hover:text-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)] disabled:opacity-60"
		title="Refresh now (R)"
		aria-label="Refresh now"
	>
		<RefreshCw class="h-3.5 w-3.5 {refreshing ? 'animate-spin' : ''}" />
	</button>
</div>
