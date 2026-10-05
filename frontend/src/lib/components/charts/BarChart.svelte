<script lang="ts">
	import { formatBytes } from '#lib/utils';

	interface Bar {
		label: string;
		value: number;
		color?: string;
	}

	let { bars, height = 300 }: { bars: Bar[]; height?: number } = $props();

	const maxVal = $derived(Math.max(...bars.map((b) => b.value), 1));
	const barHeight = $derived(Math.max(20, Math.min(32, (height - 16) / Math.max(bars.length, 1))));

	let hoveredIndex: number | null = $state(null);
</script>

<div class="flex flex-col gap-1" style="max-height: {height}px; overflow-y: auto;">
	{#each bars as bar, i}
		<div
			class="flex items-center gap-2 rounded px-1 text-sm transition-colors {hoveredIndex === i ? 'bg-secondary/40' : ''}"
			onmouseenter={() => (hoveredIndex = i)}
			onmouseleave={() => (hoveredIndex = null)}
			role="listitem"
		>
			<span class="w-36 truncate text-right text-muted-foreground" title={bar.label}>{bar.label}</span>
			<div class="relative flex-1" style="height: {barHeight - 4}px">
				<div
					class="absolute inset-y-0 left-0 rounded-r transition-all duration-300"
					style="width: {(bar.value / maxVal) * 100}%;
						background-color: {bar.color || 'var(--color-primary)'};
						min-width: 2px;
						opacity: {hoveredIndex !== null && hoveredIndex !== i ? 0.35 : 1};
						filter: {hoveredIndex === i ? 'brightness(1.2)' : 'none'};"
				></div>
			</div>
			<span class="w-20 text-right font-mono text-xs">{formatBytes(bar.value)}</span>
		</div>
	{/each}
</div>
