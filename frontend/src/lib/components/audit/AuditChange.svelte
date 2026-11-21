<script lang="ts">
	import { diffChunks, diffStats, isPolicyEvent, lineDiff, valueText } from '#lib/audit/format';
	import type { AuditEvent } from '#lib/audit/types';

	let { event }: { event: AuditEvent } = $props();

	const before = $derived(valueText(event.old));
	const after = $derived(valueText(event.new));
	const policy = $derived(isPolicyEvent(event));
	// Short values read better side by side on one line each; long or
	// multi-line ones as a diff.
	const compact = (v: unknown) => (v === undefined || v === null ? '' : typeof v === 'string' ? v : JSON.stringify(v));
	const shortBefore = $derived(compact(event.old));
	const shortAfter = $derived(compact(event.new));
	const asDiff = $derived(policy || shortBefore.includes('\n') || shortAfter.includes('\n') || shortBefore.length + shortAfter.length > 160);
	const lines = $derived(asDiff ? lineDiff(before, after) : []);
	const chunks = $derived(diffChunks(lines, 3));
	const stats = $derived(diffStats(lines));
	let showAll = $state(false);
</script>

{#if before === '' && after === ''}
	<p class="text-xs text-muted-foreground">This event has no old or new value.</p>
{:else if !asDiff}
	<div class="grid grid-cols-1 gap-2 text-xs sm:grid-cols-2">
		<div class="rounded-md border border-red-500/30 bg-red-500/5 p-2">
			<div class="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">Old</div>
			<pre class="whitespace-pre-wrap break-all font-mono">{shortBefore || '—'}</pre>
		</div>
		<div class="rounded-md border border-green-500/30 bg-green-500/5 p-2">
			<div class="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">New</div>
			<pre class="whitespace-pre-wrap break-all font-mono">{shortAfter || '—'}</pre>
		</div>
	</div>
{:else}
	<div class="rounded-md border border-border">
		<div class="flex items-center justify-between gap-2 border-b border-border bg-secondary/40 px-2 py-1 text-xs">
			<span class="font-medium">{policy ? 'Policy file diff' : 'Diff'}</span>
			<span class="flex items-center gap-2">
				<span class="tabular-nums text-green-700 dark:text-green-400">+{stats.added}</span>
				<span class="tabular-nums text-red-700 dark:text-red-400">−{stats.removed}</span>
				<button class="rounded border border-border px-1.5 py-0.5 hover:bg-secondary" onclick={() => (showAll = !showAll)}>
					{showAll ? 'Changes only' : 'Whole file'}
				</button>
			</span>
		</div>
		<div class="max-h-[60vh] overflow-auto font-mono text-[11px] leading-5" data-testid="audit-diff">
			{#snippet row(line: (typeof lines)[number])}
				<div
					class="grid grid-cols-[2.5rem_2.5rem_1rem_minmax(0,1fr)] {line.kind === 'add'
						? 'bg-green-500/10'
						: line.kind === 'del'
							? 'bg-red-500/10'
							: ''}"
				>
					<span class="select-none pr-1 text-right text-muted-foreground/70">{line.oldNo ?? ''}</span>
					<span class="select-none pr-1 text-right text-muted-foreground/70">{line.newNo ?? ''}</span>
					<span class="select-none text-center {line.kind === 'add' ? 'text-green-700 dark:text-green-400' : line.kind === 'del' ? 'text-red-700 dark:text-red-400' : 'text-muted-foreground/50'}">
						{line.kind === 'add' ? '+' : line.kind === 'del' ? '−' : ' '}
					</span>
					<span class="whitespace-pre pr-2">{line.text}</span>
				</div>
			{/snippet}
			{#if showAll}
				{#each lines as line}
					{@render row(line)}
				{/each}
			{:else}
				{#each chunks as chunk}
					{#if chunk.kind === 'skip'}
						<div class="bg-secondary/40 px-2 text-muted-foreground">⋯ {chunk.count} unchanged {chunk.count === 1 ? 'line' : 'lines'}</div>
					{:else}
						{#each chunk.lines as line}
							{@render row(line)}
						{/each}
					{/if}
				{/each}
			{/if}
		</div>
	</div>
{/if}
