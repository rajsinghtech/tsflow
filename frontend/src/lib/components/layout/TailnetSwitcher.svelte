<script lang="ts">
	import { ChevronDown } from 'lucide-svelte';
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { refreshTailnetStatus, selectTailnet, selectedTailnetId, tailnets } from '#lib/stores/tailnet-store';

	let open = $state(false);
	// Last id written to the address bar. Stops the URL sync from running again
	// when SvelteKit reports the same navigation back to this component.
	let urlTailnet = $state<string | null>(null);

	const selected = $derived($tailnets.find((tailnet) => tailnet.id === $selectedTailnetId) ?? null);
	const statusLabel = $derived.by(() => {
		if (!selected) return '';
		if (selected.poller.lastError) return selected.poller.lastError;
		return selected.poller.running ? 'Poller running' : 'Poller stopped';
	});

	// Keep the choice in the address bar without dropping other query params.
	$effect(() => {
		const chosen = $selectedTailnetId;
		if (!chosen || urlTailnet === chosen) return;
		urlTailnet = chosen;
		const current = new URL(window.location.href);
		if (current.searchParams.get('tailnet') === chosen) return;
		current.searchParams.set('tailnet', chosen);
		void replaceState(current, page.state);
	});

	// Back and forward restore the tailnet from the URL.
	$effect(() => {
		const fromUrl = page.url.searchParams.get('tailnet');
		if (!fromUrl || fromUrl === $selectedTailnetId) return;
		if (!$tailnets.some((tailnet) => tailnet.id === fromUrl)) return;
		urlTailnet = fromUrl;
		void selectTailnet(fromUrl);
	});

	function toggle() {
		open = !open;
		if (open) void refreshTailnetStatus();
	}

	async function pick(id: string) {
		open = false;
		await selectTailnet(id);
	}

	function handleWindowClick(event: MouseEvent) {
		if (open && !(event.target as Element).closest('.tailnet-switcher')) {
			open = false;
		}
	}
</script>

<svelte:window onclick={handleWindowClick} />

{#if $tailnets.length > 1 && selected}
	<div class="tailnet-switcher relative">
		<button
			type="button"
			onclick={toggle}
			class="flex h-8 max-w-36 items-center gap-1.5 rounded-md border border-border bg-background px-2 text-sm hover:bg-secondary sm:h-9 sm:max-w-52"
			aria-haspopup="listbox"
			aria-expanded={open}
			aria-label="Tailnet"
			title={statusLabel}
		>
			<span
				class="h-1.5 w-1.5 shrink-0 rounded-full {selected.poller.lastError
					? 'bg-destructive'
					: selected.poller.running
						? 'bg-node-private'
						: 'bg-muted-foreground'}"
			></span>
			<span class="truncate">{selected.displayName}</span>
			<ChevronDown class="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
		</button>

		{#if open}
			<div
				class="absolute top-full left-0 z-50 mt-1 w-64 rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-xl"
				role="listbox"
				aria-label="Tailnets"
			>
				{#each $tailnets as tailnet (tailnet.id)}
					<button
						type="button"
						role="option"
						aria-selected={tailnet.id === selected.id}
						onclick={() => pick(tailnet.id)}
						class="flex w-full flex-col items-start gap-0.5 rounded-md px-2 py-1.5 text-left text-sm hover:bg-secondary"
						class:bg-secondary={tailnet.id === selected.id}
					>
						<span class="truncate">{tailnet.displayName}</span>
						{#if tailnet.displayName !== tailnet.id}
							<span class="text-[11px] text-muted-foreground">{tailnet.id}</span>
						{/if}
						{#if tailnet.poller.lastError}
							<span class="text-[11px] text-destructive">{tailnet.poller.lastError}</span>
						{:else if tailnet.id === selected.id}
							<span class="text-[11px] text-muted-foreground">
								{tailnet.poller.running ? 'Poller running' : 'Poller stopped'}
							</span>
						{/if}
					</button>
				{/each}
			</div>
		{/if}
	</div>
{/if}
