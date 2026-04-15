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
	import { uiStore, themeStore } from '$lib/stores';
	import { highlightedEdgeIds, hasSelection } from '$lib/stores/ui-store';
	import { applyElkLayout } from '$lib/utils/elk-layout';
	import type { NetworkNode as NetworkNodeType, NetworkLink } from '$lib/types';
	import NetworkNode from './NetworkNode.svelte';


	interface Props {
		nodes: NetworkNodeType[];
		edges: NetworkLink[];
	}

	let { nodes, edges }: Props = $props();

	const nodeTypes = {
		network: NetworkNode as unknown as typeof NetworkNode
	};


	// Get edge style based on traffic type and selection state
	function getEdgeStyle(edge: NetworkLink, dimmed: boolean = false): string {
		let strokeColor = 'var(--color-muted-foreground)';

		{
			switch (edge.trafficType) {
				case 'virtual':
					strokeColor = 'var(--color-traffic-virtual)';
					break;
				case 'subnet':
					strokeColor = 'var(--color-traffic-subnet)';
					break;
				case 'physical':
					strokeColor = 'var(--color-traffic-physical)';
					break;
			}
		}

		// Width based on traffic volume
		let strokeWidth = 1;
		if (edge.totalBytes > 10000000) strokeWidth = 4;
		else if (edge.totalBytes > 1000000) strokeWidth = 3;
		else if (edge.totalBytes > 100000) strokeWidth = 2;

		const opacity = dimmed ? 0.15 : 1;
		return `stroke: ${strokeColor}; stroke-width: ${strokeWidth}px; opacity: ${opacity};`;
	}

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

	// Track topology (node IDs only — edge churn should not trigger full re-layout)
	let lastTopologyKey = '';
	let isLayouting = $state(false);
	let hasInitialLayout = false;
	let layoutVersion = 0;
	let layoutDebounceTimer: ReturnType<typeof setTimeout> | null = null;

	// Store references to flow functions (set by child component)
	let fitBoundsRef: ((bounds: { x: number; y: number; width: number; height: number }, options?: { duration?: number; padding?: number }) => void) | null = null;
	let fitViewRef: ((options?: { duration?: number; padding?: number }) => void) | null = null;

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

	// Build a topology key from node IDs only — edge churn (new flows appearing/disappearing)
	// should not trigger expensive full re-layouts
	function buildTopologyKey(nodeList: NetworkNodeType[]): string {
		return nodeList.map((n) => n.id).sort().join(',');
	}

	// Update stores and apply layout when props change
	$effect(() => {
		const currentTopologyKey = buildTopologyKey(nodes);
		const currentEdgeKey = edges.map((e) => `${e.id}:${e.totalBytes}`).sort().join(',');

		if (currentTopologyKey !== lastTopologyKey && nodes.length > 0) {
			// Node set changed - debounce re-layout to batch rapid changes
			lastTopologyKey = currentTopologyKey;
			lastEdgeKey = currentEdgeKey;
			originalEdges = edges;

			if (layoutDebounceTimer) clearTimeout(layoutDebounceTimer);
			layoutDebounceTimer = setTimeout(() => {
				layoutDebounceTimer = null;
				layoutNodes();
			}, 100);
		} else if (currentEdgeKey !== lastEdgeKey && !isLayouting) {
			// Only traffic volumes or edge set changed - update edges without re-layout
			lastEdgeKey = currentEdgeKey;
			originalEdges = edges;
			const highlighted = $highlightedEdgeIds;
			const isSelectionActive = $hasSelection;

			// Sync edges: add new edges, remove stale ones, update styles
			const newEdgeMap = new Map(edges.map((e) => [e.id, e]));
			flowEdgesStore.update((currentEdges) => {
				const existingIds = new Set(currentEdges.map((e) => e.id));
				// Update existing edges + keep only edges that still exist
				const updated = currentEdges
					.filter((flowEdge) => newEdgeMap.has(flowEdge.id))
					.map((flowEdge) => {
						const originalEdge = newEdgeMap.get(flowEdge.id)!;
						const dimmed = isSelectionActive && !highlighted.has(flowEdge.id);
						return {
							...flowEdge,
							source: originalEdge.source,
							target: originalEdge.target,
							style: getEdgeStyle(originalEdge, dimmed)
						};
					});
				// Add new edges
				for (const [id, edge] of newEdgeMap) {
					if (!existingIds.has(id)) {
						const dimmed = isSelectionActive && !highlighted.has(id);
						updated.push({
							id: edge.id,
							source: edge.source,
							target: edge.target,
							type: 'default',
							style: getEdgeStyle(edge, dimmed)
						});
					}
				}
				return updated;
			});

			// Also update node data (traffic totals etc.) without changing positions
			const nodeDataMap = new Map(nodes.map((n) => [n.id, n]));
			flowNodesStore.update((currentNodes) => {
				return currentNodes.map((flowNode) => {
					const freshData = nodeDataMap.get(flowNode.id);
					if (!freshData) return flowNode;
					return {
						...flowNode,
						data: { label: freshData.displayName, ...freshData }
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

	async function layoutNodes() {
		if (isLayouting) return;
		isLayouting = true;
		const thisVersion = ++layoutVersion;

		try {
			// Capture existing positions so we can preserve them on subsequent refreshes
			const existingPositions = new Map<string, { x: number; y: number }>();
			get(flowNodesStore).forEach((n) => {
				existingPositions.set(n.id, { x: n.position.x, y: n.position.y });
			});
			const isInitial = !hasInitialLayout;

			// Convert to Svelte Flow format
			const flowNodes: Node[] = nodes.map((node) => ({
				id: node.id,
				type: 'network',
				position: existingPositions.get(node.id) ?? { x: 0, y: 0 },
				data: {
					label: node.displayName,
					...node
				}
			}));

			const flowEdges: Edge[] = edges.map((edge) => ({
				id: edge.id,
				source: edge.source,
				target: edge.target,
				type: 'default',
				style: getEdgeStyle(edge)
			}));

			if (isInitial) {
				// First layout: full ELK layout + fitView
				const { nodes: layoutedNodes, edges: layoutedEdges } = await applyElkLayout(
					flowNodes,
					flowEdges,
					{ algorithm: 'layered', nodeSpacing: 150 }
				);

				if (thisVersion !== layoutVersion) return; // stale layout, discard

				flowNodesStore.set(layoutedNodes);
				flowEdgesStore.set(layoutedEdges);
				hasInitialLayout = true;
			} else {
				// Subsequent refresh: preserve existing node positions, place new nodes
				const newNodeIds = nodes
					.filter((n) => !existingPositions.has(n.id))
					.map((n) => n.id);

				if (newNodeIds.length > 0 && newNodeIds.length < nodes.length * 0.5) {
					// Minor topology change: run ELK for full graph but then
					// restore positions for existing nodes (only use ELK for new ones)
					const { nodes: layoutedNodes, edges: layoutedEdges } = await applyElkLayout(
						flowNodes,
						flowEdges,
						{ algorithm: 'layered', nodeSpacing: 150 }
					);

					if (thisVersion !== layoutVersion) return;

					const newNodeSet = new Set(newNodeIds);
					const mergedNodes = layoutedNodes.map((ln) => {
						if (newNodeSet.has(ln.id)) return ln; // new node gets ELK position
						const existing = existingPositions.get(ln.id);
						if (existing) return { ...ln, position: existing }; // keep existing
						return ln;
					});

					flowNodesStore.set(mergedNodes);
					flowEdgesStore.set(layoutedEdges);
				} else if (newNodeIds.length >= nodes.length * 0.5) {
					// Major topology change (>50% new): full re-layout
					const { nodes: layoutedNodes, edges: layoutedEdges } = await applyElkLayout(
						flowNodes,
						flowEdges,
						{ algorithm: 'layered', nodeSpacing: 150 }
					);

					if (thisVersion !== layoutVersion) return;

					flowNodesStore.set(layoutedNodes);
					flowEdgesStore.set(layoutedEdges);
				} else {
					// No new nodes, just nodes were removed — update in place
					if (thisVersion !== layoutVersion) return;

					flowNodesStore.set(flowNodes);
					flowEdgesStore.set(flowEdges);
				}
			}
		} catch (error) {
			if (thisVersion !== layoutVersion) return;
			console.error('Layout failed:', error);
			// Fallback: just set nodes with grid positions
			const cols = Math.ceil(Math.sqrt(nodes.length));
			const flowNodes: Node[] = nodes.map((node, index) => ({
				id: node.id,
				type: 'network',
				position: {
					x: (index % cols) * 300 + 50,
					y: Math.floor(index / cols) * 180 + 50
				},
				data: {
					label: node.displayName,
					...node
				}
			}));

			const flowEdges: Edge[] = edges.map((edge) => ({
				id: edge.id,
				source: edge.source,
				target: edge.target,
				type: 'default',
				style: getEdgeStyle(edge)
			}));

			flowNodesStore.set(flowNodes);
			flowEdgesStore.set(flowEdges);
			if (!hasInitialLayout) hasInitialLayout = true;
		} finally {
			if (thisVersion === layoutVersion) {
				isLayouting = false;
			}
		}
	}

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

	// Capture flow instance when mounted, only fitView on initial layout
	function captureFlowInstance() {
		const { fitBounds, fitView } = useSvelteFlow();
		fitBoundsRef = fitBounds;
		fitViewRef = fitView;
		requestAnimationFrame(() => {
			fitView({ duration: 300, padding: 0.1 });
		});
	}
</script>

<div class="h-full w-full relative">
	{#if isLayouting && !hasInitialLayout}
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
				proOptions={{ hideAttribution: true }}
				onnodeclick={handleNodeClick}
				onedgeclick={handleEdgeClick}
				onpaneclick={handlePaneClick}
				oninit={captureFlowInstance}
			>
				<Background />
				<Controls />
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
			</SvelteFlow>
		</SvelteFlowProvider>
	{/if}

	<!-- Layout update indicator (shown as overlay, does NOT unmount the graph) -->
	{#if isLayouting && hasInitialLayout}
		<div class="absolute top-2 left-1/2 -translate-x-1/2 z-10 rounded-md bg-card/90 border border-border px-3 py-1.5 text-xs text-muted-foreground shadow-sm">
			Updating layout...
		</div>
	{/if}
</div>
