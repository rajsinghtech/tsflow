<script lang="ts">
	import { formatBytes } from '#lib/utils';

	interface Segment {
		label: string;
		value: number;
		color: string;
	}

	let { segments, size = 200, strokeWidth = 32 }: { segments: Segment[]; size?: number; strokeWidth?: number } = $props();

	const radius = $derived((size - strokeWidth) / 2);
	const circumference = $derived(2 * Math.PI * radius);
	const center = $derived(size / 2);
	const total = $derived(segments.reduce((sum, s) => sum + s.value, 0));

	let hoveredIndex: number | null = $state(null);

	const arcs = $derived.by(() => {
		if (total === 0) return [];
		let offset = 0;
		return segments.filter(s => s.value > 0).map((s, i) => {
			const pct = s.value / total;
			const dashLen = circumference * pct;
			const dashOff = circumference * offset;
			offset += pct;
			return { ...s, pct, dashLen, dashOff, index: i };
		});
	});
</script>

<div class="flex items-center gap-4">
	<svg width={size} height={size} viewBox="0 0 {size} {size}" class="shrink-0">
		{#if total === 0}
			<circle cx={center} cy={center} r={radius} fill="none"
				stroke="currentColor" stroke-width={strokeWidth} class="text-muted/20" />
			<text x={center} y={center + 4} text-anchor="middle" class="fill-muted-foreground" style="font-size: 12px;">
				No data
			</text>
		{:else}
			{#each arcs as arc}
				<circle cx={center} cy={center} r={radius} fill="none"
					stroke={arc.color}
					stroke-width={hoveredIndex === arc.index ? strokeWidth + 6 : strokeWidth}
					stroke-dasharray="{arc.dashLen} {circumference - arc.dashLen}"
					stroke-dashoffset={-arc.dashOff}
					transform="rotate(-90 {center} {center})"
					class="transition-all duration-200 cursor-pointer"
					style="opacity: {hoveredIndex !== null && hoveredIndex !== arc.index ? 0.4 : 1};"
					role="img"
					aria-label="{arc.label}: {(arc.pct * 100).toFixed(1)}%"
					onmouseenter={() => (hoveredIndex = arc.index)}
					onmouseleave={() => (hoveredIndex = null)} />
			{/each}
			<!-- Center text on hover -->
			{#if hoveredIndex !== null && arcs[hoveredIndex]}
				{@const h = arcs[hoveredIndex]}
				<text x={center} y={center - 8} text-anchor="middle" class="fill-foreground text-sm font-medium" style="font-size: 14px;">
					{h.label}
				</text>
				<text x={center} y={center + 10} text-anchor="middle" class="fill-muted-foreground" style="font-size: 12px;">
					{formatBytes(h.value)}
				</text>
				<text x={center} y={center + 26} text-anchor="middle" class="fill-muted-foreground" style="font-size: 11px;">
					{(h.pct * 100).toFixed(1)}%
				</text>
			{:else}
				<text x={center} y={center + 4} text-anchor="middle" class="fill-foreground text-sm font-medium" style="font-size: 14px;">
					{formatBytes(total)}
				</text>
			{/if}
		{/if}
	</svg>
	<div class="flex flex-col gap-1.5 text-sm">
		{#each arcs as arc}
			<button
				class="flex items-center gap-2 rounded px-1 py-0.5 text-left transition-colors hover:bg-secondary/50 {hoveredIndex === arc.index ? 'bg-secondary/50' : ''}"
				onmouseenter={() => (hoveredIndex = arc.index)}
				onmouseleave={() => (hoveredIndex = null)}
			>
				<div class="h-3 w-3 shrink-0 rounded-sm" style="background-color: {arc.color}"></div>
				<span class="text-muted-foreground">{arc.label}</span>
				<span class="font-medium">{(arc.pct * 100).toFixed(1)}%</span>
				<span class="text-xs text-muted-foreground/70">{formatBytes(arc.value)}</span>
			</button>
		{/each}
	</div>
</div>
