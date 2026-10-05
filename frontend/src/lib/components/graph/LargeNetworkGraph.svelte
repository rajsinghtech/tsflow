<script lang="ts">
	import { formatBytes } from '#lib/utils';
	import { uiStore } from '#lib/stores';
	import { hasSelection, highlightedNodeIds } from '#lib/stores/ui-store';
	import type { NetworkLink, NetworkNode } from '#lib/types';
	import {
		LARGE_LAYOUT_SPACING,
		boundsOf,
		buildRenderModel,
		cullToViewport,
		placeNodes,
		type PlacedNode,
		type RenderEdge
	} from '#lib/graph/aggregate';
	import { edgeAppearance } from '#lib/graph/full-graph';
	import { layoutOffThread, type LayoutEngine } from '#lib/graph/layout-client';

	interface Props {
		nodes: NetworkNode[];
		edges: NetworkLink[];
	}

	let { nodes, edges }: Props = $props();

	let paneEl = $state<HTMLDivElement | null>(null);
	let paneWidth = $state(0);
	let paneHeight = $state(0);
	let offsetX = $state(40);
	let offsetY = $state(40);
	let zoom = $state(1);
	let placed = $state<PlacedNode[]>([]);
	let renderEdges = $state<RenderEdge[]>([]);
	let deviceCount = $state(0);
	let groupCount = $state(0);
	let layoutPending = $state(true);
	let layoutEngine = $state<LayoutEngine>('main');
	let expanded = $state<{ id: string; label: string }[]>([]);
	let needsFit = false;
	let hasPlaced = false;
	let requestToken = 0;

	let drag: { x: number; y: number; ox: number; oy: number; moved: boolean } | null = null;

	const viewport = $derived.by(() => ({
		x: -offsetX / zoom,
		y: -offsetY / zoom,
		width: (paneWidth || 1280) / zoom,
		height: (paneHeight || 800) / zoom
	}));

	const visible = $derived(cullToViewport(placed, renderEdges, viewport));
	const visibleById = $derived(new Map(visible.nodes.map((node) => [node.id, node])));

	function fit(items: PlacedNode[]) {
		if (items.length === 0 || paneWidth === 0 || paneHeight === 0) return;
		const bounds = boundsOf(items);
		const padX = 36;
		const padTop = 76;
		const padBottom = 28;
		let nextZoom = Math.min(
			(paneWidth - padX * 2) / bounds.width,
			(paneHeight - padTop - padBottom) / bounds.height,
			1.25
		);
		const pitchX = 220 + LARGE_LAYOUT_SPACING;
		const pitchY = 120 + LARGE_LAYOUT_SPACING;
		const cells = (paneWidth / nextZoom / pitchX) * (paneHeight / nextZoom / pitchY);
		if (cells > 300) nextZoom *= Math.sqrt(cells / 300);
		// A fully expanded group can be hundreds of cards. Keep them readable
		// instead of shrinking the whole tailnet onto one screen.
		if (nextZoom < 0.45) nextZoom = 0.7;
		nextZoom = Math.min(Math.max(nextZoom, 0.2), 1.5);
		zoom = nextZoom;
		offsetX = (paneWidth - bounds.width * nextZoom) / 2 - bounds.x * nextZoom;
		offsetY =
			(paneHeight - bounds.height * nextZoom) / 2 - bounds.y * nextZoom + (padTop - padBottom) / 2;
	}

	$effect(() => {
		const token = ++requestToken;
		const expandedIds = new Set(expanded.map((item) => item.id));
		const model = buildRenderModel(nodes, edges, expandedIds);
		layoutPending = !hasPlaced;
		layoutOffThread(
			model.nodes.map((node) => ({ id: node.id, width: node.width, height: node.height })),
			LARGE_LAYOUT_SPACING
		).then((result) => {
			if (token !== requestToken) return;
			placed = placeNodes(model.nodes, result.positions);
			renderEdges = model.edges;
			deviceCount = model.deviceCount;
			groupCount = model.groupCount;
			layoutEngine = result.engine;
			hasPlaced = true;
			layoutPending = false;
			if (paneWidth > 0 && paneHeight > 0) fit(placed);
			else needsFit = true;
		});
		return () => {
			requestToken += 1;
		};
	});

	$effect(() => {
		if (!needsFit || paneWidth === 0 || placed.length === 0) return;
		needsFit = false;
		fit(placed);
	});

	function expandGroup(node: PlacedNode) {
		expanded = [...expanded, { id: node.id, label: node.label }];
	}

	function collapseGroup(id: string) {
		expanded = expanded.filter((item) => item.id !== id);
	}

	function onPointerDown(event: PointerEvent) {
		if (event.button !== 0) return;
		const target = event.target as HTMLElement | null;
		if (target?.closest('[data-node-id], [data-graph-control]')) return;
		drag = { x: event.clientX, y: event.clientY, ox: offsetX, oy: offsetY, moved: false };
		(event.currentTarget as HTMLElement).setPointerCapture(event.pointerId);
	}

	function onPointerMove(event: PointerEvent) {
		if (!drag) return;
		const dx = event.clientX - drag.x;
		const dy = event.clientY - drag.y;
		if (Math.abs(dx) + Math.abs(dy) > 3) drag.moved = true;
		offsetX = drag.ox + dx;
		offsetY = drag.oy + dy;
	}

	function onPointerUp() {
		if (!drag) return;
		const moved = drag.moved;
		drag = null;
		if (!moved) uiStore.clearSelection();
	}

	function onWheel(event: WheelEvent) {
		event.preventDefault();
		const rect = paneEl?.getBoundingClientRect();
		if (!rect) return;
		const sx = event.clientX - rect.left;
		const sy = event.clientY - rect.top;
		const worldX = (sx - offsetX) / zoom;
		const worldY = (sy - offsetY) / zoom;
		const next = Math.min(2.5, Math.max(0.2, zoom * (event.deltaY > 0 ? 0.92 : 1.08)));
		zoom = next;
		offsetX = sx - worldX * next;
		offsetY = sy - worldY * next;
	}

	function nodeCenter(node: PlacedNode) {
		return { x: node.x + node.width / 2, y: node.y + node.height / 2 };
	}
</script>

<div
	class="relative h-full w-full overflow-hidden bg-background"
	data-graph-mode="aggregated"
	data-mounted={visible.nodes.length}
	data-devices={deviceCount || nodes.length}
	data-groups={groupCount}
	data-layout-engine={layoutEngine}
	data-layout-pending={layoutPending}
	bind:this={paneEl}
	bind:clientWidth={paneWidth}
	bind:clientHeight={paneHeight}
	onpointerdown={onPointerDown}
	onpointermove={onPointerMove}
	onpointerup={onPointerUp}
	onwheel={onWheel}
	role="application"
	aria-label="Aggregated network graph"
>
	<div
		class="absolute left-3 top-3 z-10 max-w-md rounded-md border border-border bg-card/95 px-3 py-2 text-sm shadow"
		data-graph-control
	>
		<div>
			{deviceCount.toLocaleString()} devices in {groupCount.toLocaleString()} groups. Click a group to expand.
		</div>
		{#if expanded.length > 0}
			<div class="mt-2 flex flex-wrap gap-1">
				{#each expanded as item (item.id)}
					<button
						type="button"
						class="rounded border border-border bg-secondary px-2 py-0.5 text-xs"
						onclick={() => collapseGroup(item.id)}
					>
						Collapse {item.label}
					</button>
				{/each}
			</div>
		{/if}
	</div>

	<div
		class="pointer-events-none absolute inset-0"
		style="background-image: radial-gradient(circle, var(--color-border) 1px, transparent 1px); background-size: 16px 16px;"
	></div>

	{#if layoutPending && placed.length === 0}
		<div class="flex h-full items-center justify-center text-muted-foreground">Calculating layout...</div>
	{:else}
		<div
			class="absolute left-0 top-0"
			style="transform: translate({offsetX}px, {offsetY}px) scale({zoom}); transform-origin: 0 0;"
		>
			<svg class="pointer-events-none absolute left-0 top-0 overflow-visible">
				{#each visible.edges as edge (edge.id)}
					{@const source = visibleById.get(edge.source)}
					{@const target = visibleById.get(edge.target)}
					{#if source && target}
						{@const from = nodeCenter(source)}
						{@const to = nodeCenter(target)}
						{@const appearance = edgeAppearance(edge)}
						<line
							x1={from.x}
							y1={from.y}
							x2={to.x}
							y2={to.y}
							stroke={appearance.color}
							stroke-width={appearance.width}
							opacity={appearance.opacity}
						/>
					{/if}
				{/each}
			</svg>

			{#each visible.nodes as node (node.id)}
				{@const dimmed = node.deviceId ? $hasSelection && !$highlightedNodeIds.has(node.deviceId) : false}
				<button
					type="button"
					data-node-id={node.id}
					class="absolute rounded-lg border-2 bg-card px-3 py-2 text-left shadow-md shadow-black/10 transition-opacity"
					class:opacity-25={dimmed}
					style="left: {node.x}px; top: {node.y}px; width: {node.width}px; height: {node.height}px; border-color: {node.kind === 'group'
						? 'var(--color-primary)'
						: 'var(--color-node-tailscale)'};"
					onclick={(event) => {
						event.stopPropagation();
						if (node.kind === 'group') expandGroup(node);
						else if (node.deviceId) uiStore.selectNode(node.deviceId);
					}}
				>
					<div class="text-[10px] uppercase tracking-wide text-muted-foreground">
						{node.kind === 'group' ? node.groupKind : 'device'}
					</div>
					<div class="truncate text-sm font-semibold text-foreground" title={node.label}>{node.label}</div>
					<div class="truncate text-xs text-muted-foreground">{node.sublabel}</div>
					<div class="mt-1 text-xs font-medium text-node-private">{formatBytes(node.totalBytes)}</div>
				</button>
			{/each}
		</div>
	{/if}

	<div class="absolute bottom-4 left-4 z-10 flex flex-col overflow-hidden rounded-md border border-border bg-card" data-graph-control>
		<button type="button" class="px-2 py-1 text-sm hover:bg-secondary" onclick={() => (zoom = Math.min(2.5, zoom * 1.2))} aria-label="Zoom in">+</button>
		<button type="button" class="border-t border-border px-2 py-1 text-sm hover:bg-secondary" onclick={() => (zoom = Math.max(0.2, zoom / 1.2))} aria-label="Zoom out">−</button>
		<button type="button" class="border-t border-border px-2 py-1 text-xs hover:bg-secondary" onclick={() => fit(placed)} aria-label="Fit view">Fit</button>
	</div>
</div>
