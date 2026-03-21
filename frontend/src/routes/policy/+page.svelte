<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { Loader2, AlertCircle, FileCode, X } from 'lucide-svelte';
	import Header from '$lib/components/layout/Header.svelte';
	import PolicyGraph from '$lib/components/policy/PolicyGraph.svelte';
	import PolicyInput from '$lib/components/policy/PolicyInput.svelte';
	import AccessQuery from '$lib/components/policy/AccessQuery.svelte';
	import PolicyFilters from '$lib/components/policy/PolicyFilters.svelte';
	import PolicyLegend from '$lib/components/policy/PolicyLegend.svelte';
	import {
		policyGraph,
		parseErrors,
		parseSummary,
		isParsing,
		filteredGraph,
		fetchError,
		fetchAndRenderPolicy,
		runQuery,
		clearQuery
	} from '$lib/stores/policy-store';
	import { policyNodesToXYFlow, policyEdgesToXYFlow } from '$lib/utils/policy-layout';

	onMount(async () => {
		// Auto-fetch policy from API on page load
		if (!$policyGraph) {
			await fetchAndRenderPolicy();
		}

		// Handle cross-link query params
		const params = $page.url.searchParams;
		const q = params.get('query');
		const d = params.get('direction') as 'inbound' | 'outbound' | null;
		if (q && $policyGraph) {
			runQuery(q, d ?? 'outbound');
		}
	});

	const xyNodes = $derived(policyNodesToXYFlow($filteredGraph.nodes));
	const xyEdges = $derived(policyEdgesToXYFlow($filteredGraph.edges));

	let showSidebar = $state(true);
	let mobileDrawerOpen = $state(false);
	let sidebarWidth = $state(320);
	let isResizing = $state(false);

	function toggleFilters() {
		if (typeof window !== 'undefined' && window.innerWidth >= 1024) {
			showSidebar = !showSidebar;
		} else {
			mobileDrawerOpen = !mobileDrawerOpen;
		}
	}

	function handleResizeStart(e: PointerEvent) {
		isResizing = true;
		(e.target as HTMLElement).setPointerCapture(e.pointerId);
	}

	function handleResizeMove(e: PointerEvent) {
		if (!isResizing) return;
		sidebarWidth = Math.max(240, Math.min(600, e.clientX));
	}

	function handleResizeEnd() {
		isResizing = false;
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return;
		if (e.key === 'Escape') {
			if (mobileDrawerOpen) {
				mobileDrawerOpen = false;
			} else {
				clearQuery();
			}
		} else if (e.key === 'f' && !e.metaKey && !e.ctrlKey) {
			toggleFilters();
		}
	}
</script>

<svelte:window onkeydown={handleKeydown} onpointermove={handleResizeMove} onpointerup={handleResizeEnd} />

<div class="flex h-screen flex-col bg-background">
	<Header />

	<div class="relative flex flex-1 overflow-hidden">
		{#if showSidebar}
			<aside
				class="hidden shrink-0 overflow-y-auto bg-card lg:block"
				style="width: {sidebarWidth}px"
			>
				<div class="flex flex-col gap-4 p-4">
					{@render sidebarContent()}
				</div>
			</aside>
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div
				class="hidden w-1 cursor-col-resize lg:block {isResizing ? 'bg-primary' : 'bg-border hover:bg-primary/50'}"
				onpointerdown={handleResizeStart}
				aria-hidden="true"
			></div>
		{/if}

		{#if mobileDrawerOpen}
			<div
				role="button"
				tabindex="0"
				class="fixed inset-0 z-40 bg-black/50 lg:hidden"
				onclick={() => (mobileDrawerOpen = false)}
				onkeydown={(e) => e.key === 'Escape' && (mobileDrawerOpen = false)}
				aria-label="Close drawer"
			></div>
			<aside class="fixed inset-y-0 left-0 z-50 flex w-72 max-w-[90vw] flex-col overflow-y-auto bg-card shadow-2xl lg:hidden">
				<div class="flex items-center justify-between border-b border-border px-4 py-3">
					<h2 class="text-sm font-semibold">Policy</h2>
					<button onclick={() => (mobileDrawerOpen = false)} class="rounded-md p-2 hover:bg-secondary">
						<X class="h-5 w-5" />
					</button>
				</div>
				<div class="flex flex-col gap-4 p-4">
					{@render sidebarContent()}
				</div>
			</aside>
		{/if}

		<main class="relative flex flex-1 flex-col overflow-hidden">
			{#if $isParsing}
				<div class="flex flex-1 flex-col items-center justify-center gap-4">
					<Loader2 class="h-8 w-8 animate-spin text-primary" />
					<p class="text-muted-foreground">Parsing policy...</p>
				</div>
			{:else if $parseErrors.length > 0 && !$policyGraph?.nodes.length}
				<div class="flex flex-1 flex-col items-center justify-center gap-4 p-4">
					<AlertCircle class="h-8 w-8 text-destructive" />
					<div class="text-center">
						<p class="font-medium text-destructive">Failed to parse policy</p>
						{#each $parseErrors as error}
							<p class="mt-1 text-sm text-muted-foreground">{error}</p>
						{/each}
					</div>
				</div>
			{:else if !$policyGraph}
				<div class="flex flex-1 flex-col items-center justify-center gap-4 p-4">
					<FileCode class="h-12 w-12 text-muted-foreground/30" />
					<div class="text-center">
						<p class="text-sm font-medium text-muted-foreground">No policy loaded</p>
						<p class="mt-1 text-xs text-muted-foreground/60">
							Paste your Tailscale policy JSON in the sidebar to visualize access rules
						</p>
					</div>
				</div>
			{:else if $filteredGraph.nodes.length === 0}
				<div class="flex flex-1 flex-col items-center justify-center gap-4 p-4">
					<p class="text-sm text-muted-foreground">No visible nodes — check your filter settings</p>
				</div>
			{:else}
				<PolicyGraph nodes={xyNodes} edges={xyEdges} />
			{/if}

			{#if $policyGraph}
				<div class="absolute bottom-3 left-3 text-xs text-muted-foreground/60">
					{$filteredGraph.nodes.length} nodes · {$filteredGraph.edges.length} edges · ELK layered
				</div>
			{/if}
		</main>
	</div>
</div>

{#snippet sidebarContent()}
	<PolicyInput />
	<AccessQuery />
	{#if $policyGraph}
		<div class="border-t border-border pt-3">
			<PolicyFilters />
		</div>
		<div class="border-t border-border pt-3">
			<h3 class="mb-1.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Status</h3>
			<div class="flex gap-3 text-xs">
				<span class={$parseSummary.errorCount > 0 ? 'text-destructive' : 'text-muted-foreground'}>
					{$parseSummary.errorCount} errors
				</span>
				<span class={$parseSummary.warningCount > 0 ? 'text-yellow-500' : 'text-muted-foreground'}>
					{$parseSummary.warningCount} warnings
				</span>
			</div>
			<div class="mt-1 text-xs text-muted-foreground">
				{$parseSummary.nodeCount} nodes · {$parseSummary.edgeCount} edges
			</div>
			{#if $policyGraph.warnings.length > 0}
				<div class="mt-2 max-h-24 space-y-1 overflow-y-auto">
					{#each $policyGraph.warnings.slice(0, 5) as warning}
						<div class="rounded bg-yellow-500/10 px-2 py-1 text-[10px] text-yellow-600 dark:text-yellow-400">
							{warning.message}
						</div>
					{/each}
					{#if $policyGraph.warnings.length > 5}
						<div class="text-[10px] text-muted-foreground">+{$policyGraph.warnings.length - 5} more</div>
					{/if}
				</div>
			{/if}
		</div>
		<div class="border-t border-border pt-3">
			<PolicyLegend />
		</div>
	{/if}
{/snippet}
