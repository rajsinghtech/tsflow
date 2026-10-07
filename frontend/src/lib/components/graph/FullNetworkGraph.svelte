<script lang="ts">
	import { writable, get } from 'svelte/store';
	import {
		SvelteFlow,
		SvelteFlowProvider,
		Background,
		Controls,
		MiniMap,
		useSvelteFlow,
		type Node,
		type Edge,
		type ColorMode
	} from '@xyflow/svelte';
	import '@xyflow/svelte/dist/style.css';
	import { uiStore, themeStore, searchMatchedNodeIds } from '#lib/stores';
	import { highlightedEdgeIds, hasSelection } from '#lib/stores/ui-store';
	import { applyElkLayout, type LayoutMemory } from '#lib/utils/elk-layout';
	import { refitAfterLayout } from '#lib/graph/layout-reuse';
	import { edgeStyle as getEdgeStyle, toFlowElements } from '#lib/graph/full-graph';
	import type { NetworkNode as NetworkNodeType, NetworkLink } from '#lib/types';
	import NetworkNode from './NetworkNode.svelte';


	interface Props {
		nodes: NetworkNodeType[];
		edges: NetworkLink[];
	}

	let { nodes, edges }: Props = $props();

	const nodeTypes = {
		network: NetworkNode as unknown as typeof NetworkNode
	};

	const MINIMAP_RENDER_LIMIT = 2000;

	// Keep track of original edges for style updates (use $state.raw for reference tracking)
	let originalEdges = $state.raw<NetworkLink[]>([]);

	// Pre-built map for O(1) edge lookups instead of O(n) .find() in .map()
	const originalEdgeMap = $derived(new Map(originalEdges.map((e) => [e.id, e])));

	// Map our theme to xyflow colorMode
	const colorMode = $derived.by((): ColorMode => {
		const mode = $themeStore;
		if (mode === 'system') return 'system';
		if (mode === 'light') return 'light';
		return 'dark';
	});

	// Cache CSS variable values for MiniMap to avoid expensive getComputedStyle calls
	// Uses a simple memoization pattern outside of Svelte's reactive system
	let colorCacheTheme: string | null = null;
	let colorCacheValues: Record<string, string> | null = null;

	function getNodeColors(): Record<string, string> {
		const currentTheme = $themeStore;
		// Return cached if theme hasn't changed
		if (colorCacheValues && colorCacheTheme === currentTheme) {
			return colorCacheValues;
		}
		// Compute and cache
		const defaults = { derp: '#8b5cf6', tailscale: '#3b82f6', private: '#10b981', public: '#f59e0b' };
		if (typeof document === 'undefined') {
			colorCacheValues = defaults;
			colorCacheTheme = currentTheme;
			return defaults;
		}
		const style = getComputedStyle(document.documentElement);
		colorCacheValues = {
			derp: style.getPropertyValue('--color-node-derp').trim() || '#8b5cf6',
			tailscale: style.getPropertyValue('--color-node-tailscale').trim() || '#3b82f6',
			private: style.getPropertyValue('--color-node-private').trim() || '#10b981',
			public: style.getPropertyValue('--color-node-public').trim() || '#f59e0b'
		};
		colorCacheTheme = currentTheme;
		return colorCacheValues;
	}

	// Create writable stores for SvelteFlow
	const flowNodesStore = writable<Node[]>([]);
	const flowEdgesStore = writable<Edge[]>([]);

	// Track topology (node IDs + edge connections, excluding traffic volumes)
	let lastTopologyKey = '';
	let isLayouting = $state(false);
	let layoutDebounceTimer: ReturnType<typeof setTimeout> | null = null;
	let layoutRunning = false;
	let layoutAgain = false;
	const layoutMemory: LayoutMemory = { boxes: new Map() };

	// Store references to flow functions (set by child component)
	let fitBoundsRef: ((bounds: { x: number; y: number; width: number; height: number }, options?: { duration?: number; padding?: number }) => void) | null = null;
	let fitViewRef: ((options?: { duration?: number; padding?: number }) => void) | null = null;
	let lastSearchFocus = '';

	// Focus zoom on selected node and its connections
	function focusOnSelection(nodeIds: string[]) {
		if (nodeIds.length === 0 || !fitBoundsRef) return;

		const currentNodes = get(flowNodesStore);
		const nodesToFit = currentNodes.filter((node) => nodeIds.includes(node.id));
		if (nodesToFit.length === 0) return;

		// Calculate bounding box
		const padding = 100;
		let minX = Infinity,
			minY = Infinity,
			maxX = -Infinity,
			maxY = -Infinity;

		nodesToFit.forEach((node) => {
			const nodeWidth = (node.width as number) || 280;
			const nodeHeight = (node.height as number) || 140;

			minX = Math.min(minX, node.position.x);
			minY = Math.min(minY, node.position.y);
			maxX = Math.max(maxX, node.position.x + nodeWidth);
			maxY = Math.max(maxY, node.position.y + nodeHeight);
		});

		const width = maxX - minX + padding * 2;
		const height = maxY - minY + padding * 2;

		fitBoundsRef(
			{
				x: minX - padding,
				y: minY - padding,
				width,
				height
			},
			{ duration: 600, padding: 0.1 }
		);
	}

	// Track edge traffic data for style-only updates
	let lastEdgeKey = '';

	function hashPart(value: string): number {
		let hash = 2166136261;
		for (let i = 0; i < value.length; i++) {
			hash ^= value.charCodeAt(i);
			hash = Math.imul(hash, 16777619);
		}
		return hash >>> 0;
	}

	function addHash(acc: { sum: number; xor: number }, value: string) {
		const h = hashPart(value);
		acc.sum = (acc.sum + h) >>> 0;
		acc.xor = (acc.xor ^ h) >>> 0;
	}

	function hashKey(parts: { count: number; sum: number; xor: number }): string {
		return `${parts.count}:${parts.sum.toString(36)}:${parts.xor.toString(36)}`;
	}

	// Build a compact topology key from node IDs + edge connections (ignoring traffic volumes)
	function buildTopologyKey(nodeList: NetworkNodeType[], edgeList: NetworkLink[]): string {
		const nodeHash = { count: nodeList.length, sum: 0, xor: 0 };
		const edgeHash = { count: edgeList.length, sum: 0, xor: 0 };
		for (const node of nodeList) addHash(nodeHash, node.id);
		for (const edge of edgeList) addHash(edgeHash, `${edge.source}->${edge.target}|${edge.trafficType}`);
		return `${hashKey(nodeHash)}|${hashKey(edgeHash)}`;
	}

	function buildEdgeTrafficKey(edgeList: NetworkLink[]): string {
		const edgeHash = { count: edgeList.length, sum: 0, xor: 0 };
		for (const edge of edgeList) {
			addHash(edgeHash, `${edge.id}:${edge.totalBytes}:${edge.txBytes}:${edge.rxBytes}`);
		}
		return hashKey(edgeHash);
	}

	// Update stores and apply layout when props change
	$effect(() => {
		const currentTopologyKey = buildTopologyKey(nodes, edges);
		const currentEdgeKey = buildEdgeTrafficKey(edges);

		if (currentTopologyKey !== lastTopologyKey && nodes.length > 0) {
			// Topology changed - debounce re-layout to batch rapid changes
			lastTopologyKey = currentTopologyKey;
			lastEdgeKey = currentEdgeKey;
			originalEdges = edges;

			if (layoutDebounceTimer) clearTimeout(layoutDebounceTimer);
			layoutDebounceTimer = setTimeout(() => {
				layoutDebounceTimer = null;
				layoutNodes();
			}, 100);
		} else if (currentEdgeKey !== lastEdgeKey && !isLayouting) {
			// Only traffic volumes changed - update edge styles without re-layout
			lastEdgeKey = currentEdgeKey;
			originalEdges = edges;
			const highlighted = $highlightedEdgeIds;
			const isSelectionActive = $hasSelection;

			const edgeLookup = new Map(edges.map((e) => [e.id, e]));
			flowEdgesStore.update((currentEdges) => {
				return currentEdges.map((flowEdge) => {
					const originalEdge = edgeLookup.get(flowEdge.id);
					if (!originalEdge) return flowEdge;

					const dimmed = isSelectionActive && !highlighted.has(flowEdge.id);
					return {
						...flowEdge,
						style: getEdgeStyle(originalEdge, dimmed)
					};
				});
			});
		} else {
			originalEdges = edges;
		}
	});

	// Cleanup debounce timer on destroy
	$effect(() => {
		return () => {
			if (layoutDebounceTimer) clearTimeout(layoutDebounceTimer);
		};
	});

	// Track pending style update during layout
	let pendingStyleUpdate = $state(false);

	// Update edge styles when selection changes
	$effect(() => {
		const highlighted = $highlightedEdgeIds;
		const isSelectionActive = $hasSelection;
		const edgeLookup = originalEdgeMap;

		// Only update if we have edges
		if (originalEdges.length === 0) return;

		// If currently layouting, mark that we need to update styles after
		if (isLayouting) {
			pendingStyleUpdate = true;
			return;
		}

		flowEdgesStore.update((currentEdges) => {
			return currentEdges.map((flowEdge) => {
				const originalEdge = edgeLookup.get(flowEdge.id);
				if (!originalEdge) return flowEdge;

				const dimmed = isSelectionActive && !highlighted.has(flowEdge.id);
				return {
					...flowEdge,
					style: getEdgeStyle(originalEdge, dimmed)
				};
			});
		});
	});

	// Apply pending style updates after layout completes
	$effect(() => {
		if (!isLayouting && pendingStyleUpdate && originalEdges.length > 0) {
			pendingStyleUpdate = false;
			const highlighted = $highlightedEdgeIds;
			const isSelectionActive = $hasSelection;
			const edgeLookup = originalEdgeMap;

			flowEdgesStore.update((currentEdges) => {
				return currentEdges.map((flowEdge) => {
					const originalEdge = edgeLookup.get(flowEdge.id);
					if (!originalEdge) return flowEdge;

					const dimmed = isSelectionActive && !highlighted.has(flowEdge.id);
					return {
						...flowEdge,
						style: getEdgeStyle(originalEdge, dimmed)
					};
				});
			});
		}
	});

	async function layoutOnce() {
		const built = toFlowElements(nodes, edges);
		const laid = await applyElkLayout(built.nodes, built.edges, { algorithm: 'layered', nodeSpacing: 150 }, layoutMemory);
		const hadPicture = get(flowNodesStore).length > 0;
		flowNodesStore.set(laid.nodes);
		flowEdgesStore.set(laid.edges);
		const matchIds = [...get(searchMatchedNodeIds)];
		if (matchIds.length > 0) {
			lastSearchFocus = matchIds.slice().sort().join(',');
			setTimeout(() => focusOnSelection(matchIds), 50);
		}
		if (refitAfterLayout({ hadPicture, reused: laid.reused, focusing: matchIds.length > 0 })) {
			requestAnimationFrame(() => fitViewRef?.({ duration: 300, padding: 0.1 }));
		}
	}

	// One layout at a time. A refresh that arrives while ELK is running is
	// folded into a single follow-up, using whatever the props are by then.
	async function layoutNodes() {
		if (layoutRunning) {
			layoutAgain = true;
			return;
		}
		layoutRunning = true;
		isLayouting = true;
		try {
			do {
				layoutAgain = false;
				try {
					await layoutOnce();
				} catch (error) {
					console.error('Layout failed:', error);
					const cols = Math.ceil(Math.sqrt(nodes.length));
					const built = toFlowElements(nodes, edges);
					flowNodesStore.set(
						built.nodes.map((node, index) => ({
							...node,
							position: {
								x: (index % cols) * 300 + 50,
								y: Math.floor(index / cols) * 180 + 50
							}
						}))
					);
					flowEdgesStore.set(built.edges);
				}
			} while (layoutAgain);
		} finally {
			layoutRunning = false;
			isLayouting = false;
		}
	}

	$effect(() => {
		const ids = [...$searchMatchedNodeIds].sort();
		const key = ids.join(',');
		if (isLayouting || key === lastSearchFocus) return;
		lastSearchFocus = key;
		if (key) focusOnSelection(ids);
	});

	function handleNodeClick({ node }: { node: Node; event: MouseEvent | TouchEvent }) {
		const nodeId = node?.id;
		if (nodeId) {
			uiStore.selectNode(nodeId);

			// Get connected nodes and focus on them
			const currentEdges = get(flowEdgesStore);
			const connectedNodeIds = new Set<string>([nodeId]);

			currentEdges.forEach((edge) => {
				if (edge.source === nodeId || edge.target === nodeId) {
					connectedNodeIds.add(edge.source);
					connectedNodeIds.add(edge.target);
				}
			});

			// Focus on selection after a brief delay for state update
			setTimeout(() => focusOnSelection(Array.from(connectedNodeIds)), 50);
		}
	}

	function handleEdgeClick({ edge }: { edge: Edge; event: MouseEvent }) {
		if (edge) {
			uiStore.selectEdge(edge.id);

			// Focus on the two connected nodes
			setTimeout(() => focusOnSelection([edge.source, edge.target]), 50);
		}
	}

	function handlePaneClick() {
		uiStore.clearSelection();
		// Reset view to show all nodes
		if (fitViewRef) fitViewRef({ duration: 400, padding: 0.1 });
	}

	// Capture flow instance when mounted, defer fitView for smoother rendering
	function captureFlowInstance() {
		const { fitBounds, fitView } = useSvelteFlow();
		fitBoundsRef = fitBounds;
		fitViewRef = fitView;
		requestAnimationFrame(() => {
			fitView({ duration: 300, padding: 0.1 });
		});
	}
</script>

<div class="h-full w-full" data-graph-mode="full" data-mounted={$flowNodesStore.length} data-devices={nodes.length}>
	{#if $flowNodesStore.length === 0 && (isLayouting || nodes.length > 0)}
		<div class="flex h-full items-center justify-center">
			<div class="text-muted-foreground">Calculating layout...</div>
		</div>
	{:else}
		<SvelteFlowProvider>
			<SvelteFlow
				nodes={$flowNodesStore}
				edges={$flowEdgesStore}
				{nodeTypes}
				{colorMode}
				minZoom={0.01}
				maxZoom={10}
				onlyRenderVisibleElements={true}
				proOptions={{ hideAttribution: true }}
				onnodeclick={handleNodeClick}
				onedgeclick={handleEdgeClick}
				onpaneclick={handlePaneClick}
				oninit={captureFlowInstance}
			>
				<Background />
				<Controls />
				{#if $flowNodesStore.length <= MINIMAP_RENDER_LIMIT}
					<MiniMap
						width={120}
						height={80}
						nodeColor={(node) => {
							const data = node.data as any;
							const colors = getNodeColors();
							if (data?.tags?.includes('derp')) return colors.derp;
							if (data?.isTailscale) return colors.tailscale;
							if (data?.tags?.includes('private')) return colors.private;
							return colors.public;
						}}
					/>
				{/if}
			</SvelteFlow>
		</SvelteFlowProvider>
	{/if}
</div>
