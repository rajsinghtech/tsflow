<script lang="ts">
	import { Repeat } from 'lucide-svelte';

	interface Props {
		color: string;
		tooltip?: string;
		ariaLabel?: string;
		decorative?: boolean;
	}

	let { color, tooltip = '', ariaLabel = tooltip, decorative = false }: Props = $props();

	let open = $state(false);
	let top = $state(0);
	let left = $state(0);
	let tipEl = $state<HTMLSpanElement | null>(null);

	// The graph pane is transformed and clips overflow, so the note is moved to
	// document.body and positioned in viewport coordinates.
	function portal(node: HTMLElement) {
		document.body.appendChild(node);
		return {
			destroy() {
				node.remove();
			}
		};
	}

	function place(anchor: HTMLElement) {
		const rect = anchor.getBoundingClientRect();
		const width = Math.min(256, window.innerWidth - 16);
		let nextLeft = rect.left;
		if (nextLeft + width > window.innerWidth - 8) {
			nextLeft = Math.max(8, window.innerWidth - 8 - width);
		}
		top = rect.bottom + 6;
		left = nextLeft;
		open = true;

		requestAnimationFrame(() => {
			if (!tipEl) return;
			const tipRect = tipEl.getBoundingClientRect();
			if (tipRect.right > window.innerWidth - 8) {
				left = Math.max(8, window.innerWidth - 8 - tipRect.width);
			}
			if (tipRect.bottom > window.innerHeight - 8) {
				top = Math.max(8, rect.top - tipRect.height - 6);
			}
		});
	}

	function show(event: MouseEvent | FocusEvent) {
		const el = event.currentTarget;
		if (el instanceof HTMLElement) place(el);
	}

	function hide() {
		open = false;
	}
</script>

{#if decorative}
	<span
		class="inline-flex shrink-0 items-center gap-0.5 rounded-full border px-1.5 py-px text-[10px] font-semibold leading-none"
		style="color: {color}; border-color: {color}; background: color-mix(in srgb, {color} 22%, var(--color-card));"
		aria-hidden="true"
	>
		<Repeat class="h-3 w-3 shrink-0" />
		self
	</span>
{:else}
	<button
		type="button"
		class="self-access-badge nodrag nopan relative inline-flex shrink-0 cursor-help items-center gap-0.5 rounded-full border px-1.5 py-px text-[10px] font-semibold leading-none focus:outline-none focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2"
		style="color: {color}; border-color: {color}; background: color-mix(in srgb, {color} 22%, var(--color-card)); outline-color: {color};"
		aria-label={ariaLabel}
		onmouseenter={show}
		onmouseleave={hide}
		onfocus={show}
		onblur={hide}
	>
		<Repeat class="h-3 w-3 shrink-0" aria-hidden="true" />
		<span aria-hidden="true">self</span>
	</button>
	{#if open && tooltip}
		<span
			bind:this={tipEl}
			role="tooltip"
			aria-hidden="true"
			class="tip"
			style="top: {top}px; left: {left}px;"
			use:portal
		>
			{tooltip}
		</span>
	{/if}
{/if}

<style>
	.tip {
		position: fixed;
		z-index: 10000;
		max-width: min(16rem, calc(100vw - 16px));
		padding: 6px 8px;
		border: 1px solid var(--color-border);
		border-radius: 6px;
		background: var(--color-popover);
		color: var(--color-popover-foreground);
		font-size: 12px;
		font-weight: 500;
		line-height: 1.4;
		text-align: left;
		white-space: normal;
		pointer-events: none;
		box-shadow: 0 8px 20px rgb(0 0 0 / 0.18);
	}
</style>
