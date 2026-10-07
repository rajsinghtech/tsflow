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
	import { uiStore, themeStore, searchMatchedNodeIds } from '#lib/stores';
	import type { NetworkLink, NetworkNode as NetworkNodeType } from '#lib/types';
	import { runElkGraph, runElkLayout, type LayoutMemory } from '#lib/utils/elk-layout';
	import { rememberLayout } from '#lib/graph/layout-reuse';
	import { boundsOf, buildRenderModel, cullToViewport, groupIdsContaining } from '#lib/graph/aggregate';
	import {
		GROUP_LAYOUT_OPTIONS,
		buildCompoundGraph,
		modelToFlow,
		sceneFromCompound,
		type LayoutBox
	} from '#lib/graph/elk-place';
	import NetworkNode from './NetworkNode.svelte';
	import GroupNode from './GroupNode.svelte';
	import ClusterNode from './ClusterNode.svelte';

	interface Props {
		nodes: NetworkNodeType[];
		edges: NetworkLink[];
	}

	let { nodes, edges }: Props = $props();

	const nodeTypes = {
		network: NetworkNode as unknown as typeof NetworkNode,
		group: GroupNode as unknown as typeof GroupNode,
		cluster: ClusterNode as unknown as typeof ClusterNode
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
	let flatNodes: Node[] = [];
	let flatEdges: Edge[] = [];
	let fitTimer: ReturnType<typeof setTimeout> | null = null;
	let layoutTimer: ReturnType<typeof setTimeout> | null = null;
	const collapsedMemory: LayoutMemory = { boxes: new Map() };

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

	function nodeBox(node: Node, origin = { x: 0, y: 0 }): LayoutBox {
		return {
			id: node.id,
			x: origin.x + node.position.x,
			y: origin.y + node.position.y,
			width: (node.width as number) || 220,
			height: (node.height as number) || 120
		};
	}

	function absoluteBoxes(list: Node[]): LayoutBox[] {
		const parents = new Map(list.filter((node) => !node.parentId).map((node) => [node.id, node]));
		return list.map((node) => {
			const parent = node.parentId ? parents.get(node.parentId) : undefined;
			return nodeBox(node, parent ? { x: parent.position.x, y: parent.position.y } : { x: 0, y: 0 });
		});
	}

	$effect(() => {
		const boxes = absoluteBoxes(sceneNodes);
		const culled = cullToViewport(boxes, sceneEdges, viewport);
		const key = culled.nodes.map((node) => `${node.id}:${node.x}:${node.y}`).join('|');
		if (key === mountedKey) return;
		mountedKey = key;
		const ids = new Set(culled.nodes.map((node) => node.id));
		for (const node of sceneNodes) {
			if (node.parentId && ids.has(node.id)) ids.add(node.parentId);
		}
		for (const node of sceneNodes) {
			if (node.parentId && ids.has(node.parentId)) ids.add(node.id);
		}
		flowNodesStore.set(sceneNodes.filter((node) => ids.has(node.id)));
		flowEdgesStore.set(sceneEdges.filter((edge) => ids.has(edge.source) && ids.has(edge.target)));
	});

	function cameraFor(boxes: LayoutBox[], duration: number) {
		if (paneWidth === 0 || paneHeight === 0 || boxes.length === 0) return;
		const bounds = boundsOf(boxes);
		const next = getViewportForBounds(bounds, paneWidth, paneHeight, 0.02, 1.25, 0.12);
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

	async function layoutGroups(
		nodeList: NetworkNodeType[],
		edgeList: NetworkLink[],
		matched: ReadonlySet<string>
	) {
		const token = ++requestToken;
		const started = performance.now();
		layoutPending = sceneNodes.length === 0;
		const devices = new Map(nodeList.map((node) => [node.id, node]));
		const collapsed = buildRenderModel(nodeList, edgeList, new Set());
		const openIds = groupIdsContaining(collapsed, matched);
		const model =
			openIds.length > 0 ? buildRenderModel(nodeList, edgeList, new Set(openIds)) : collapsed;
		const collapsedFlow = modelToFlow(collapsed, devices, matched);
		const flow = model === collapsed ? collapsedFlow : modelToFlow(model, devices, matched);
		const collapsedLaid = await runElkLayout(
			collapsedFlow.nodes,
			collapsedFlow.edges,
			GROUP_LAYOUT_OPTIONS,
			collapsedMemory
		);
		if (token !== requestToken) return;
		rememberLayout(collapsedMemory, collapsedLaid.nodes);
		let laid = collapsedLaid;
		if (flow !== collapsedFlow) {
			laid = await runElkLayout(flow.nodes, flow.edges, GROUP_LAYOUT_OPTIONS);
			if (token !== requestToken) return;
		}
		const keptPositions = flow === collapsedFlow && collapsedLaid.reused;
		const boxes = absoluteBoxes(laid.nodes);
		const focusIds = new Set(matched);
		for (const node of laid.nodes) {
			const data = node.data as { searchMatch?: boolean };
			if (data?.searchMatch) focusIds.add(node.id);
		}
		const focus = boxes.filter((box) => focusIds.has(box.id));
		const fit = focus.length > 0 ? focus : boxes;
		if ((!keptPositions || focus.length > 0) && paneWidth > 0 && paneHeight > 0) {
			const next = getViewportForBounds(
				fit.length ? boundsOf(fit) : { x: 0, y: 0, width: 1, height: 1 },
				paneWidth,
				paneHeight,
				0.02,
				1.25,
				0.12
			);
			viewX = next.x;
			viewY = next.y;
			viewZoom = next.zoom;
			flowApi?.setViewport(next, { duration: focus.length > 0 ? 600 : 300 });
		}
		sceneNodes = laid.nodes;
		sceneEdges = laid.edges;
		flatNodes = collapsedLaid.nodes;
		flatEdges = collapsedLaid.edges;
		for (const node of collapsedLaid.nodes) {
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
		expanded = openIds.map((id) => {
			const group = collapsed.nodes.find((node) => node.id === id);
			return { id, label: group?.label || id };
		});
		layoutPending = false;
		if (firstMs === 0) firstMs = Math.round(performance.now() - started);
	}

	$effect(() => {
		const matched = $searchMatchedNodeIds;
		const key = `${topologyKey(nodes, edges)}|${[...matched].sort().join(',')}`;
		if (key === sourceKey) return;
		sourceKey = key;
		if (layoutTimer) clearTimeout(layoutTimer);
		layoutTimer = setTimeout(() => {
			layoutTimer = null;
			void layoutGroups(nodes, edges, matched);
		}, 100);
	});

	$effect(() => {
		return () => {
			if (layoutTimer) clearTimeout(layoutTimer);
		};
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
		const graph = buildCompoundGraph(model, devices, groupId, [...newMemberIds]);
		const laid = await runElkGraph(graph);
		if (token !== requestToken) return;
		const scene = sceneFromCompound(laid, groupId, home, model, devices, {
			displayName: data.displayName || groupId,
			groupKind: (group.data as { groupKind?: string }).groupKind,
			memberCount: data.memberCount ?? newMemberIds.size
		});
		animating = true;
		sceneNodes = scene.nodes;
		sceneEdges = scene.edges;
		deviceCount = model.deviceCount;
		groupCount = model.groupCount;
		expanded = [...expanded, { id: groupId, label: data.displayName || groupId }];
		expandMs = Math.round(performance.now() - started);
		await tick();
		cameraFor(
			scene.nodes.filter((node) => !node.parentId).map((node) => nodeBox(node)),
			600
		);
		if (fitTimer) clearTimeout(fitTimer);
		fitTimer = setTimeout(() => {
			animating = false;
		}, 650);
	}

	function collapseGroup(_groupId: string) {
		sceneNodes = flatNodes.map((node) => ({ ...node, position: { ...node.position } }));
		sceneEdges = flatEdges;
		const model = buildRenderModel(nodes, edges, new Set());
		groupCount = model.groupCount;
		expanded = [];
		cameraFor(
			sceneNodes.filter((node) => !node.parentId).map((node) => nodeBox(node)),
			400
		);
	}

	function handleNodeClick({ node }: { node: Node; event: MouseEvent | TouchEvent }) {
		const data = node.data as { kind?: string };
		if (data?.kind === 'group') {
			void expandGroup(node.id);
			return;
		}
		if (data?.kind === 'cluster') return;
		uiStore.selectNode(node.id);
	}

	function handlePaneClick() {
		uiStore.clearSelection();
		cameraFor(
			sceneNodes.filter((node) => !node.parentId).map((node) => nodeBox(node)),
			400
		);
	}

	function onMove(_event: MouseEvent | TouchEvent | null, next: { x: number; y: number; zoom: number }) {
		viewX = next.x;
		viewY = next.y;
		viewZoom = next.zoom;
	}

	function onInit() {
		const api = useSvelteFlow();
		flowApi = api;
		if (sceneNodes.length > 0) {
			cameraFor(
				sceneNodes.filter((node) => !node.parentId).map((node) => nodeBox(node)),
				300
			);
		}
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
				minZoom={0.02}
				maxZoom={2}
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
