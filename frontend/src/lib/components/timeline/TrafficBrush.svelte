<script lang="ts">
	import {
		applyBrushDrag,
		brushHit,
		formatStamp,
		nudgeBrush,
		timeToRatio,
		type BrushAction
	} from './time-window';
	import { formatBps, niceCeiling, peakBin, ratePaths, type RateBin } from './throughput';
	import { formatBytes } from '#lib/utils/format';

	let {
		bins,
		domainStart,
		domainEnd,
		selectionStart,
		selectionEnd,
		onChange,
		label = 'Traffic overview',
		tall = true,
		rate = null,
		formatTime = (time: number) => formatStamp(time)
	}: {
		bins: { start?: number; end?: number; bytes: number | null }[];
		domainStart: number;
		domainEnd: number;
		selectionStart: number;
		selectionEnd: number;
		onChange: (start: number, end: number) => void;
		label?: string;
		tall?: boolean;
		// Draws a throughput chart (bits per second) instead of byte bars.
		rate?: RateBin[] | null;
		formatTime?: (time: number) => string;
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

	// Chart geometry in the 0..100 x 0..40 viewBox. The top leaves room for the axis label.
	const FLOOR = 38;
	const HEIGHT = 32;
	const rateTop = $derived(rate ? niceCeiling(Math.max(0, ...rate.map((bin) => bin.bps ?? 0))) : 1);
	const paths = $derived(rate ? ratePaths(rate, rateTop, HEIGHT, FLOOR) : { area: '', line: '' });
	const peak = $derived(rate ? peakBin(rate) : null);
	const gridLines = $derived(rate ? [1, 0.5].map((fraction) => ({ y: FLOOR - HEIGHT * fraction, value: rateTop * fraction })) : []);

	const gradientId = `rate-fill-${Math.random().toString(36).slice(2, 8)}`;

	// Hover readout. Mouse and pen only, so a tap on a phone still just selects.
	let hoverRatio = $state<number | null>(null);
	const hover = $derived.by(() => {
		if (hoverRatio === null || drag || domain <= 0) return null;
		const list = rate ?? bins;
		if (!list.length) return null;
		const index = Math.min(list.length - 1, Math.floor(hoverRatio * list.length));
		const time = domainStart + hoverRatio * domain;
		if (rate) {
			const bin = rate[index];
			const y = bin.bps === null ? null : FLOOR - (Math.min(bin.bps, rateTop) / rateTop) * HEIGHT;
			return { ratio: hoverRatio, time, value: bin.bps === null ? 'No data' : formatBps(bin.bps), y };
		}
		const bin = bins[index];
		return { ratio: hoverRatio, time: bin.start ?? time, value: bin.bytes === null ? 'No data' : formatBytes(bin.bytes), y: null };
	});

	function track(event: PointerEvent) {
		if (event.pointerType === 'touch' || !tall) return;
		hoverRatio = ratioFromEvent(event);
	}

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
	class="relative touch-none rounded-md bg-muted/50 {tall ? (rate ? 'h-20' : 'h-16') : 'h-3'}"
	role="group"
	aria-label={label}
	onpointerdown={(event) => begin('create', event)}
	onpointermove={(event) => {
		move(event);
		track(event);
	}}
	onpointerleave={() => (hoverRatio = null)}
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
	{#if rate}
		<svg class="absolute inset-0 h-full w-full" viewBox="0 0 100 40" preserveAspectRatio="none" aria-hidden="true">
			{#each gridLines as line}
				<line x1="0" x2="100" y1={line.y} y2={line.y} class="stroke-border" stroke-width="1" vector-effect="non-scaling-stroke" stroke-dasharray="2 3" />
			{/each}
			<line x1="0" x2="100" y1={FLOOR} y2={FLOOR} class="stroke-border" stroke-width="1" vector-effect="non-scaling-stroke" />
			<defs>
				<linearGradient id={gradientId} x1="0" x2="0" y1="0" y2="1">
					<stop offset="0%" stop-color="var(--color-primary)" stop-opacity="0.32" />
					<stop offset="100%" stop-color="var(--color-primary)" stop-opacity="0.03" />
				</linearGradient>
			</defs>
			<path d={paths.area} fill="url(#{gradientId})" data-rate-area="true" />
			<path d={paths.line} fill="none" stroke="var(--color-primary)" stroke-width="1.5" stroke-linejoin="round" vector-effect="non-scaling-stroke" />
		</svg>
		<div class="pointer-events-none absolute inset-0 text-[9px] leading-none text-muted-foreground tabular-nums" aria-hidden="true">
			{#each gridLines as line}
				<span class="absolute left-1 -translate-y-full pb-px" style="top: {(line.y / 40) * 100}%" data-axis-label="true">{formatBps(line.value)}</span>
			{/each}
		</div>
	{:else}
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
	{/if}

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
			class="absolute inset-y-1 z-20 w-3 -translate-x-1/2 cursor-ew-resize rounded-sm border border-background bg-primary pointer-coarse:before:absolute pointer-coarse:before:inset-y-0 pointer-coarse:before:-right-4 pointer-coarse:before:-left-4 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)]"
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
			class="absolute inset-y-1 z-20 w-3 -translate-x-1/2 cursor-ew-resize rounded-sm border border-background bg-primary pointer-coarse:before:absolute pointer-coarse:before:inset-y-0 pointer-coarse:before:-right-4 pointer-coarse:before:-left-4 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)]"
			style="left: {rightRatio * 100}%"
			onpointerdown={(event) => begin('end', event)}
			onkeydown={(event) => onKey('end', event)}
		></button>
	{/if}

	{#if rate && peak}
		<div
			class="pointer-events-none absolute z-10 size-2 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-background bg-primary"
			style="left: {((peak.index + 0.5) / rate.length) * 100}%; top: {((FLOOR - (Math.min(peak.bps, rateTop) / rateTop) * HEIGHT) / 40) * 100}%"
			title="Peak {formatBps(peak.bps)}"
			data-peak-marker="true"
			aria-hidden="true"
		></div>
	{/if}

	{#if hover}
		<div class="pointer-events-none absolute inset-y-0 z-30 w-px bg-foreground/40" style="left: {hover.ratio * 100}%" aria-hidden="true"></div>
		{#if hover.y !== null}
			<div
				class="pointer-events-none absolute z-30 size-1.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-primary ring-2 ring-background"
				style="left: {hover.ratio * 100}%; top: {(hover.y / 40) * 100}%"
				aria-hidden="true"
			></div>
		{/if}
		<div
			class="pointer-events-none absolute bottom-full z-40 mb-1 rounded-md border border-border bg-popover px-2 py-1 text-[11px] whitespace-nowrap text-popover-foreground shadow-sm"
			style="left: {hover.ratio * 100}%; transform: translateX(-{Math.round(hover.ratio * 100)}%)"
			role="status"
			data-chart-tooltip="true"
		>
			<span class="text-muted-foreground">{formatTime(hover.time)}</span>
			<span class="ml-1.5 font-medium tabular-nums">{hover.value}</span>
		</div>
	{/if}
</div>
