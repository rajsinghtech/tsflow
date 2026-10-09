<script lang="ts">
	import {
		applyBrushDrag,
		brushHit,
		formatStamp,
		nudgeBrush,
		timeToRatio,
		type BrushAction
	} from './time-window';

	let {
		bins,
		domainStart,
		domainEnd,
		selectionStart,
		selectionEnd,
		onChange,
		label = 'Traffic overview',
		tall = true
	}: {
		bins: { bytes: number | null }[];
		domainStart: number;
		domainEnd: number;
		selectionStart: number;
		selectionEnd: number;
		onChange: (start: number, end: number) => void;
		label?: string;
		tall?: boolean;
	} = $props();

	let chartEl: HTMLDivElement | null = $state(null);
	let drag = $state<{ start: number; end: number } | null>(null);
	let pointer: {
		action: BrushAction;
		originRatio: number;
		originStart: number;
		originEnd: number;
		pointerId: number;
	} | null = null;

	const shownStart = $derived(drag?.start ?? selectionStart);
	const shownEnd = $derived(drag?.end ?? selectionEnd);
	const maxBytes = $derived(Math.max(1, ...bins.map((bin) => bin.bytes ?? 0)));
	const domain = $derived(Math.max(0, domainEnd - domainStart));
	const overlaps = $derived(domain > 0 && shownEnd > domainStart && shownStart < domainEnd);
	const leftRatio = $derived(overlaps ? timeToRatio(Math.max(shownStart, domainStart), domainStart, domainEnd) : 0);
	const rightRatio = $derived(overlaps ? timeToRatio(Math.min(shownEnd, domainEnd), domainStart, domainEnd) : 0);

	function ratioFromEvent(event: PointerEvent): number {
		const rect = chartEl?.getBoundingClientRect();
		if (!rect || rect.width <= 0) return 0;
		return Math.min(1, Math.max(0, (event.clientX - rect.left) / rect.width));
	}

	function edgeRatio(): number {
		const width = chartEl?.getBoundingClientRect().width ?? 1;
		return Math.min(0.08, 12 / Math.max(width, 1));
	}

	function begin(action: BrushAction, event: PointerEvent) {
		if (event.button !== 0 || domain <= 0) return;
		event.preventDefault();
		event.stopPropagation();
		const ratio = ratioFromEvent(event);
		const resolved = action === 'create' && overlaps
			? brushHit(ratio, leftRatio, rightRatio, edgeRatio())
			: action;
		pointer = {
			action: resolved,
			originRatio: ratio,
			originStart: shownStart,
			originEnd: shownEnd,
			pointerId: event.pointerId
		};
		drag = { start: shownStart, end: shownEnd };
		chartEl?.setPointerCapture?.(event.pointerId);
	}

	function move(event: PointerEvent) {
		if (!pointer || pointer.pointerId !== event.pointerId) return;
		drag = applyBrushDrag({
			action: pointer.action,
			originRatio: pointer.originRatio,
			ratio: ratioFromEvent(event),
			originStart: pointer.originStart,
			originEnd: pointer.originEnd,
			domainStart,
			domainEnd
		});
	}

	function finish(event: PointerEvent) {
		if (!pointer || pointer.pointerId !== event.pointerId) return;
		const next = drag;
		const changed = !!next && (Math.abs(next.start - pointer.originStart) > 1000 || Math.abs(next.end - pointer.originEnd) > 1000 || pointer.action === 'create');
		pointer = null;
		drag = null;
		if (changed && next) onChange(next.start, next.end);
	}

	function onKey(which: 'start' | 'end', event: KeyboardEvent) {
		const next = nudgeBrush(which, event.key, event.shiftKey, shownStart, shownEnd, domainStart, domainEnd);
		if (!next) return;
		event.preventDefault();
		onChange(next.start, next.end);
	}
</script>

<div
	bind:this={chartEl}
	class="relative touch-none rounded-md bg-muted/50 {tall ? 'h-16' : 'h-3'}"
	role="group"
	aria-label={label}
	onpointerdown={(event) => begin('create', event)}
	onpointermove={move}
	onpointerup={finish}
	onpointercancel={finish}
>
	{#each bins as bin, index}
		{#if bin.bytes === null}
			<div
				class="time-gap pointer-events-none absolute inset-y-0"
				style="left: {(index / Math.max(bins.length, 1)) * 100}%; width: {100 / Math.max(bins.length, 1)}%"
				data-gap="true"
			></div>
		{/if}
	{/each}
	<svg class="absolute inset-0 h-full w-full" viewBox="0 0 100 40" preserveAspectRatio="none" aria-hidden="true">
		{#each bins as bin, index}
			{#if bin.bytes !== null && bin.bytes > 0}
				{@const width = 100 / Math.max(bins.length, 1)}
				{@const height = Math.max(1.5, (bin.bytes / maxBytes) * 34)}
				<rect
					x={index * width + width * 0.12}
					y={38 - height}
					width={Math.max(0.4, width * 0.76)}
					height={height}
					fill="var(--color-primary)"
				/>
			{/if}
		{/each}
	</svg>

	{#if overlaps}
		<div class="pointer-events-none absolute inset-y-0 left-0 bg-background/75" style="width: {leftRatio * 100}%"></div>
		<div class="pointer-events-none absolute inset-y-0 right-0 bg-background/75" style="width: {(1 - rightRatio) * 100}%"></div>
		<div
			class="absolute inset-y-1 z-10 cursor-grab rounded-sm ring-1 ring-primary/80 active:cursor-grabbing"
			style="left: {leftRatio * 100}%; width: {Math.max((rightRatio - leftRatio) * 100, 0)}%"
			aria-hidden="true"
			onpointerdown={(event) => begin('move', event)}
		></div>
		<button
			type="button"
			role="slider"
			aria-label="Window start"
			aria-valuemin={Math.round(domainStart)}
			aria-valuemax={Math.round(domainEnd)}
			aria-valuenow={Math.round(shownStart)}
			aria-valuetext={formatStamp(shownStart)}
			aria-orientation="horizontal"
			class="absolute inset-y-1 z-20 w-3 -translate-x-1/2 cursor-ew-resize rounded-sm border border-background bg-primary focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)]"
			style="left: {leftRatio * 100}%"
			onpointerdown={(event) => begin('start', event)}
			onkeydown={(event) => onKey('start', event)}
		></button>
		<button
			type="button"
			role="slider"
			aria-label="Window end"
			aria-valuemin={Math.round(domainStart)}
			aria-valuemax={Math.round(domainEnd)}
			aria-valuenow={Math.round(shownEnd)}
			aria-valuetext={formatStamp(shownEnd)}
			aria-orientation="horizontal"
			class="absolute inset-y-1 z-20 w-3 -translate-x-1/2 cursor-ew-resize rounded-sm border border-background bg-primary focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)]"
			style="left: {rightRatio * 100}%"
			onpointerdown={(event) => begin('end', event)}
			onkeydown={(event) => onKey('end', event)}
		></button>
	{/if}
</div>
