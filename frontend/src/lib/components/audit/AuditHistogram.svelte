<script lang="ts">
	import { categoryLabel } from '#lib/audit/format';
	import { bucketSpanMs } from '#lib/audit/insights';
	import type { AuditHistogram } from '#lib/audit/types';

	let {
		histogram,
		active = null,
		onpick
	}: { histogram: AuditHistogram; active?: { start: string; end: string } | null; onpick?: (start: string, end: string) => void } = $props();

	const span = $derived(bucketSpanMs(histogram));
	const max = $derived(Math.max(1, ...histogram.buckets.map((b) => b.total)));
	const total = $derived(histogram.buckets.reduce((sum, b) => sum + b.total, 0));
	const failed = $derived(histogram.buckets.reduce((sum, b) => sum + b.failed, 0));

	function label(start: string): string {
		const d = new Date(start);
		return histogram.bucket === 'hour'
			? d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
			: d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
	}

	function tooltip(b: AuditHistogram['buckets'][number]): string {
		const parts = Object.entries(b.categories ?? {})
			.sort((x, y) => y[1] - x[1])
			.map(([cat, n]) => `${categoryLabel(cat)}: ${n}`);
		return [`${label(b.start)} · ${b.total} events${b.failed ? ` (${b.failed} failed)` : ''}`, ...parts].join('\n');
	}

	function isActive(start: string): boolean {
		if (!active) return false;
		const t = new Date(start).getTime();
		return t >= new Date(active.start).getTime() && t < new Date(active.end).getTime();
	}
</script>

<div class="px-3 pb-2 pt-1 sm:px-6" data-testid="audit-histogram">
	<div class="mb-1 flex items-center justify-between text-[11px] text-muted-foreground">
		<span>Activity per {histogram.bucket}{span > (histogram.bucket === 'hour' ? 3_600_000 : 86_400_000) ? ` (${Math.round(span / (histogram.bucket === 'hour' ? 3_600_000 : 86_400_000))} ${histogram.bucket}s per bar)` : ''} · click a bar to zoom in</span>
		<span class="tabular-nums">{total.toLocaleString()} events{#if failed} · <span class="text-red-600 dark:text-red-400">{failed} failed</span>{/if}</span>
	</div>
	<div class="flex h-12 items-end gap-px">
		{#each histogram.buckets as b (b.start)}
			{@const h = b.total ? Math.max(6, (b.total / max) * 100) : 0}
			<button
				class="group relative flex h-full min-w-0 flex-1 flex-col justify-end rounded-sm hover:bg-secondary/60 {isActive(b.start) ? 'bg-primary/10' : ''}"
				title={tooltip(b)}
				aria-label={tooltip(b)}
				disabled={!b.total}
				onclick={() => onpick?.(b.start, new Date(new Date(b.start).getTime() + span).toISOString())}
			>
				{#if b.total}
					<span class="block w-full overflow-hidden rounded-sm bg-primary/60 group-hover:bg-primary" style="height: {h}%">
						{#if b.failed}<span class="block w-full bg-red-500" style="height: {(b.failed / b.total) * 100}%"></span>{/if}
					</span>
				{:else}
					<span class="block h-px w-full bg-border"></span>
				{/if}
			</button>
		{/each}
	</div>
	{#if histogram.buckets.length > 1}
		<div class="mt-0.5 flex justify-between text-[10px] text-muted-foreground">
			<span>{label(histogram.buckets[0].start)}</span>
			<span>{label(histogram.buckets[histogram.buckets.length - 1].start)}</span>
		</div>
	{/if}
</div>
