<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { Loader2, AlertCircle, FileCode, SlidersHorizontal } from 'lucide-svelte';
	import Header from '$lib/components/layout/Header.svelte';
	import PolicyGraph from '$lib/components/policy/PolicyGraph.svelte';
	import PolicyEditor from '$lib/components/policy/PolicyEditor.svelte';
	import AccessQuery from '$lib/components/policy/AccessQuery.svelte';
	import PolicyFilters from '$lib/components/policy/PolicyFilters.svelte';
	import PolicyLegend from '$lib/components/policy/PolicyLegend.svelte';
	import {
		policyGraph,
		parseErrors,
		parseSummary,
		isParsing,
		filteredGraph,
		fetchAndRenderPolicy,
		runQuery,
		clearQuery
	} from '$lib/stores/policy-store';
	import { policyNodesToXYFlow, policyEdgesToXYFlow } from '$lib/utils/policy-layout';

	onMount(async () => {
		if (!$policyGraph) {
			await fetchAndRenderPolicy();
		}
		const params = $page.url.searchParams;
		const q = params.get('query');
		const d = params.get('direction') as 'inbound' | 'outbound' | null;
		if (q && $policyGraph) {
			runQuery(q, d ?? 'outbound');
		}
	});

	const xyNodes = $derived(policyNodesToXYFlow($filteredGraph.nodes));
	const xyEdges = $derived(policyEdgesToXYFlow($filteredGraph.edges));

	// Split pane state
	let splitPercent = $state(50);
	let isResizing = $state(false);

	function handleResizeStart(e: PointerEvent) {
		isResizing = true;
		(e.target as HTMLElement).setPointerCapture(e.pointerId);
	}

	function handleResizeMove(e: PointerEvent) {
		if (!isResizing) return;
		const container = document.getElementById('split-container');
		if (!container) return;
		const rect = container.getBoundingClientRect();
		// Editor is on the right, so its width = distance from cursor to right edge
		splitPercent = Math.max(20, Math.min(80, ((rect.right - e.clientX) / rect.width) * 100));
	}

	function handleResizeEnd() {
		isResizing = false;
	}

	// Controls panel
	let showControls = $state(true);

	function handleKeydown(e: KeyboardEvent) {
		if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return;
		// Let CodeMirror handle its own keys
		if ((e.target as HTMLElement)?.closest('.cm-editor')) return;
		if (e.key === 'Escape') {
			clearQuery();
		}
	}
</script>

<svelte:window onkeydown={handleKeydown} onpointermove={handleResizeMove} onpointerup={handleResizeEnd} />

<div class="flex h-screen flex-col bg-background">
	<Header />

	<!-- Main split layout -->
	<div id="split-container" class="relative flex flex-1 overflow-hidden">
		<!-- Left: Graph area (full width, controls float over it) -->
		<div class="relative flex-1 overflow-hidden">
			{#if $isParsing}
				<div class="flex h-full flex-col items-center justify-center gap-4">
					<Loader2 class="h-8 w-8 animate-spin text-primary" />
					<p class="text-muted-foreground">Parsing policy...</p>
				</div>
			{:else if $parseErrors.length > 0 && !$policyGraph?.nodes.length}
				<div class="flex h-full flex-col items-center justify-center gap-4 p-4">
					<AlertCircle class="h-8 w-8 text-destructive" />
					<div class="text-center">
						<p class="font-medium text-destructive">Parse error</p>
						{#each $parseErrors as error}
							<p class="mt-1 text-sm text-muted-foreground">{error}</p>
						{/each}
					</div>
				</div>
			{:else if !$policyGraph}
				<div class="flex h-full flex-col items-center justify-center gap-4 p-4">
					<FileCode class="h-12 w-12 text-muted-foreground/30" />
					<p class="text-sm text-muted-foreground">Press Render or Cmd+S to visualize</p>
				</div>
			{:else if $filteredGraph.nodes.length === 0}
				<div class="flex h-full flex-col items-center justify-center gap-4 p-4">
					<p class="text-sm text-muted-foreground">No visible nodes</p>
				</div>
			{:else}
				<PolicyGraph nodes={xyNodes} edges={xyEdges} />
			{/if}

			<!-- Floating controls panel — left side, vertically centered -->
			{#if $policyGraph}
				<div class="pointer-events-none absolute inset-y-0 left-2 flex items-center">
					<div class="pointer-events-auto flex max-h-[90%] w-56 flex-col overflow-hidden rounded-lg border border-border bg-card/95 shadow-lg backdrop-blur-sm">
						<!-- Header: stats + toggle -->
						<div class="flex items-center gap-2 border-b border-border px-3 py-2">
							<div class="flex flex-1 items-center gap-2 text-[10px] text-muted-foreground">
								<span class={$parseSummary.errorCount > 0 ? 'text-destructive' : ''}>{$parseSummary.errorCount} err</span>
								<span class={$parseSummary.warningCount > 0 ? 'text-yellow-500' : ''}>{$parseSummary.warningCount} warn</span>
								<span class="ml-auto">{$filteredGraph.nodes.length}n · {$filteredGraph.edges.length}e</span>
							</div>
							<button
								onclick={() => showControls = !showControls}
								class="shrink-0 rounded p-0.5 hover:bg-secondary"
								title="{showControls ? 'Collapse' : 'Expand'} controls"
							>
								<SlidersHorizontal class="h-3.5 w-3.5 {showControls ? 'text-primary' : 'text-muted-foreground'}" />
							</button>
						</div>
						{#if showControls}
							<!-- Scrollable content -->
							<div class="flex-1 overflow-y-auto">
								<div class="border-b border-border px-3 py-3">
									<AccessQuery />
								</div>
								<div class="border-b border-border px-3 py-3">
									<PolicyFilters />
								</div>
								<div class="px-3 py-3">
									<PolicyLegend />
								</div>
							</div>
						{/if}
					</div>
				</div>
			{/if}
		</div>

		<!-- Resize handle -->
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div
			class="w-1 cursor-col-resize {isResizing ? 'bg-primary' : 'bg-border hover:bg-primary/50'}"
			onpointerdown={handleResizeStart}
			aria-hidden="true"
		></div>

		<!-- Right: Code Editor -->
		<div class="flex flex-col overflow-hidden" style="width: {splitPercent}%">
			<PolicyEditor />
		</div>
	</div>
</div>
