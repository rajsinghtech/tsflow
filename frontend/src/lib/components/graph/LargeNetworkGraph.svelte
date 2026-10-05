<script lang="ts">
	import { tick } from 'svelte';
	import { writable } from 'svelte/store';
	import {
		SvelteFlow,
		SvelteFlowProvider,
		Background,
		Controls,
		useSvelteFlow,
		getViewportForBounds,
		type Node,
		type Edge,
		type ColorMode
	} from '@xyflow/svelte';
	import '@xyflow/svelte/dist/style.css';
	import { uiStore, themeStore } from '#lib/stores';
	import type { NetworkLink, NetworkNode as NetworkNodeType } from '#lib/types';
	import { runElkLayout } from '#lib/utils/elk-layout';
	import { boundsOf, buildRenderModel, cullToViewport } from '#lib/graph/aggregate';
	import {
		GROUP_LAYOUT_OPTIONS,
		expansionSubgraph,
		modelToFlow,
		placeExpandedLayout,
		type LayoutBox
	} from '#lib/graph/elk-place';
	import NetworkNode from './NetworkNode.svelte';
	import GroupNode from './GroupNode.svelte';

	interface Props {
		nodes: NetworkNodeType[];
		edges: NetworkLink[];
	}

	let { nodes, edges }: Props = $props();

	const nodeTypes = {
		network: NetworkNode as unknown as typeof NetworkNode,
		group: GroupNode as unknown as typeof GroupNode
	};

	const flowNodesStore = writable<Node[]>([]);
	const flowEdgesStore = writable<Edge[]>([]);

	let paneWidth = $state(0);
	let paneHeight = $state(0);
	let viewX = $state(0);
	let viewY = $state(0);
	let viewZoom = $state(1);
	let sceneNodes = $state<Node[]>([]);
	let sceneEdges = $state<Edge[]>([]);
	let layoutPending = $state(true);
	let animating = $state(false);
	let deviceCount = $state(0);
	let groupCount = $state(0);
	let expanded = $state<{ id: string; label: string }[]>([]);
	let firstMs = $state(0);
	let expandMs = $state(0);
	let sourceKey = '';
	let requestToken = 0;
	let mountedKey = '';
	const groupHome = new Map<string, { x: number; y: number; width?: number; height?: number }>();
	let fitTimer: ReturnType<typeof setTimeout> | null = null;

	let flowApi: {
		setViewport: (viewport: { x: number; y: number; zoom: number }, options?: { duration?: number }) => Promise<boolean>;
		fitBounds: (
			bounds: { x: number; y: number; width: number; height: number },
			options?: { duration?: number; padding?: number }
		) => Promise<boolean>;
	} | null = null;

	const colorMode = $derived.by((): ColorMode => {
		const mode = $themeStore;
		if (mode === 'system') return 'system';
		if (mode === 'light') return 'light';
		return 'dark';
	});

	const viewport = $derived.by(() => ({
		x: -viewX / (viewZoom || 1),
		y: -viewY / (viewZoom || 1),
		width: (paneWidth || 1280) / (viewZoom || 1),
		height: (paneHeight || 800) / (viewZoom || 1)
	}));

	function nodeBox(node: Node): LayoutBox {
		return {
			id: node.id,
			x: node.position.x,
			y: node.position.y,
			width: (node.width as number) || 220,
			height: (node.height as number) || 120
		};
	}

	$effect(() => {
		const boxes = sceneNodes.map(nodeBox);
		const culled = cullToViewport(boxes, sceneEdges, viewport);
		const key = culled.nodes.map((node) => `${node.id}:${node.x}:${node.y}`).join('|');
		if (key === mountedKey) return;
		mountedKey = key;
		const ids = new Set(culled.nodes.map((node) => node.id));
		flowNodesStore.set(sceneNodes.filter((node) => ids.has(node.id)));
		flowEdgesStore.set(sceneEdges.filter((edge) => ids.has(edge.source) && ids.has(edge.target)));
	});

	function cameraFor(boxes: LayoutBox[], duration: number) {
		if (paneWidth === 0 || paneHeight === 0 || boxes.length === 0) return;
		const bounds = boundsOf(boxes);
		const next = getViewportForBounds(bounds, paneWidth, paneHeight, 0.55, 1.5, 0.15);
		viewX = next.x;
		viewY = next.y;
		viewZoom = next.zoom;
		flowApi?.setViewport(next, { duration });
	}

	function hashPart(value: string): number {
		let hash = 2166136261;
		for (let i = 0; i < value.length; i++) {
			hash ^= value.charCodeAt(i);
			hash = Math.imul(hash, 16777619);
		}
		return hash >>> 0;
	}

	function topologyKey(nodeList: NetworkNodeType[], edgeList: NetworkLink[]): string {
		let sum = 0;
		let xor = 0;
		for (const node of nodeList) {
			const hash = hashPart(node.id);
			sum = (sum + hash) >>> 0;
			xor = (xor ^ hash) >>> 0;
		}
		for (const edge of edgeList) {
			const hash = hashPart(`${edge.source}->${edge.target}|${edge.trafficType}`);
			sum = (sum + hash) >>> 0;
			xor = (xor ^ hash) >>> 0;
		}
		return `${nodeList.length}:${edgeList.length}:${sum}:${xor}`;
	}

	async function layoutGroups(nodeList: NetworkNodeType[], edgeList: NetworkLink[]) {
		const token = ++requestToken;
		const started = performance.now();
		layoutPending = sceneNodes.length === 0;
		const devices = new Map(nodeList.map((node) => [node.id, node]));
		const model = buildRenderModel(nodeList, edgeList, new Set());
		const flow = modelToFlow(model, devices);
		const laid = await runElkLayout(flow.nodes, flow.edges, GROUP_LAYOUT_OPTIONS);
		if (token !== requestToken) return;
		const boxes = laid.nodes.map(nodeBox);
		if (paneWidth > 0 && paneHeight > 0) {
			const next = getViewportForBounds(boxes.length ? boundsOf(boxes) : { x: 0, y: 0, width: 1, height: 1 }, paneWidth, paneHeight, 0.55, 1.5, 0.15);
			viewX = next.x;
			viewY = next.y;
			viewZoom = next.zoom;
			flowApi?.setViewport(next, { duration: 300 });
		}
		sceneNodes = laid.nodes;
		sceneEdges = laid.edges;
		for (const node of laid.nodes) {
			const data = node.data as { kind?: string };
			if (data?.kind === 'group') {
				groupHome.set(node.id, {
					x: node.position.x,
					y: node.position.y,
					width: node.width as number | undefined,
					height: node.height as number | undefined
				});
			}
		}
		deviceCount = model.deviceCount;
		groupCount = model.groupCount;
		expanded = [];
		layoutPending = false;
		if (firstMs === 0) firstMs = Math.round(performance.now() - started);
	}

	$effect(() => {
		const key = topologyKey(nodes, edges);
		if (key === sourceKey) return;
		sourceKey = key;
		void layoutGroups(nodes, edges);
	});

	async function expandGroup(groupId: string) {
		const group = sceneNodes.find((node) => node.id === groupId);
		const data = group?.data as { memberCount?: number; displayName?: string } | undefined;
		if (!group || data?.memberCount == null) return;
		const started = performance.now();
		const token = ++requestToken;
		const home = nodeBox(group);
		const devices = new Map(nodes.map((node) => [node.id, node]));
		const opened = new Set([...expanded.map((item) => item.id), groupId]);
		const model = buildRenderModel(nodes, edges, opened);
		const newMemberIds = new Set(
			model.nodes
				.filter((node) => node.kind === 'device' && !sceneNodes.some((existing) => existing.id === node.id))
				.map((node) => node.id)
		);
		if (newMemberIds.size === 0) return;
		const current = new Map(sceneNodes.map((node) => [node.id, node]));
		const subgraph = expansionSubgraph(model, newMemberIds, devices, current);
		const laid = await runElkLayout(subgraph.nodes, subgraph.edges, GROUP_LAYOUT_OPTIONS);
		if (token !== requestToken) return;

		const kept = new Map(
			sceneNodes.filter((node) => node.id !== groupId).map((node) => [node.id, { ...node.position }])
		);
		const placed = placeExpandedLayout(
			laid.nodes.map(nodeBox),
			kept,
			newMemberIds,
			home
		);
		const byId = new Map(laid.nodes.map((node) => [node.id, node]));
		const stable = sceneNodes.filter((node) => node.id !== groupId);
		const finals = [...newMemberIds].map((id) => {
			const node = byId.get(id)!;
			const position = placed.get(id) ?? home;
			return { ...node, position: { x: position.x, y: position.y } };
		});
		const atGroup = finals.map((node) => ({ ...node, position: { x: home.x, y: home.y } }));
		animating = true;
		sceneNodes = [...stable, ...atGroup];
		sceneEdges = modelToFlow(model, devices).edges;
		deviceCount = model.deviceCount;
		groupCount = model.groupCount;
		expanded = [...expanded, { id: groupId, label: data.displayName || groupId }];
		await tick();
		requestAnimationFrame(() => {
			sceneNodes = [...stable, ...finals];
			expandMs = Math.round(performance.now() - started);
			cameraFor(finals.map(nodeBox), 600);
			if (fitTimer) clearTimeout(fitTimer);
			fitTimer = setTimeout(() => {
				animating = false;
			}, 650);
		});
	}

	function collapseGroup(groupId: string) {
		const nextExpanded = expanded.filter((item) => item.id !== groupId);
		const devices = new Map(nodes.map((node) => [node.id, node]));
		const model = buildRenderModel(nodes, edges, new Set(nextExpanded.map((item) => item.id)));
		const flow = modelToFlow(model, devices);
		const previous = new Map(sceneNodes.map((node) => [node.id, node]));
		sceneNodes = flow.nodes.map((node) => {
			const existing = previous.get(node.id);
			if (existing) return { ...node, position: existing.position, width: existing.width, height: existing.height };
			const home = groupHome.get(node.id);
			if (home) return { ...node, position: { x: home.x, y: home.y }, width: home.width, height: home.height };
			return node;
		});
		sceneEdges = flow.edges;
		groupCount = model.groupCount;
		expanded = nextExpanded;
		const group = sceneNodes.find((node) => node.id === groupId);
		if (group) cameraFor([nodeBox(group)], 400);
	}

	function handleNodeClick({ node }: { node: Node; event: MouseEvent | TouchEvent }) {
		const data = node.data as { kind?: string };
		if (data?.kind === 'group') {
			void expandGroup(node.id);
			return;
		}
		uiStore.selectNode(node.id);
	}

	function handlePaneClick() {
		uiStore.clearSelection();
		cameraFor(sceneNodes.map(nodeBox), 400);
	}

	function onMove(_event: MouseEvent | TouchEvent | null, next: { x: number; y: number; zoom: number }) {
		viewX = next.x;
		viewY = next.y;
		viewZoom = next.zoom;
	}

	function onInit() {
		const api = useSvelteFlow();
		flowApi = api;
		if (sceneNodes.length > 0) cameraFor(sceneNodes.map(nodeBox), 300);
	}
</script>

<div
	class="large-graph relative h-full w-full"
	class:is-animating={animating}
	data-graph-mode="aggregated"
	data-mounted={$flowNodesStore.length}
	data-devices={deviceCount || nodes.length}
	data-groups={groupCount}
	data-layout-engine="elk"
	data-layout-pending={layoutPending}
	data-first-ms={firstMs}
	data-expand-ms={expandMs}
	bind:clientWidth={paneWidth}
	bind:clientHeight={paneHeight}
>
	<div class="absolute left-3 top-3 z-10 max-w-md rounded-md border border-border bg-card/95 px-3 py-2 text-sm shadow">
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

	{#if layoutPending && sceneNodes.length === 0}
		<div class="flex h-full items-center justify-center text-muted-foreground">Calculating layout...</div>
	{:else}
		<SvelteFlowProvider>
			<SvelteFlow
				nodes={$flowNodesStore}
				edges={$flowEdgesStore}
				{nodeTypes}
				{colorMode}
				minZoom={0.05}
				maxZoom={1.5}
				onlyRenderVisibleElements={true}
				proOptions={{ hideAttribution: true }}
				onnodeclick={handleNodeClick}
				onpaneclick={handlePaneClick}
				onmove={onMove}
				oninit={onInit}
			>
				<Background />
				<Controls />
			</SvelteFlow>
		</SvelteFlowProvider>
	{/if}
</div>

<style>
	:global(.large-graph.is-animating .svelte-flow__node) {
		transition: transform 300ms ease;
	}
</style>
