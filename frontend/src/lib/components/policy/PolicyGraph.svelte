<script lang="ts">
	import { writable } from 'svelte/store';
	import {
		SvelteFlow,
		SvelteFlowProvider,
		Controls,
		MiniMap,
		Background,
		useSvelteFlow,
		type Node,
		type Edge,
		type ColorMode
	} from '@xyflow/svelte';
	import '@xyflow/svelte/dist/style.css';
	import PolicyNode from './PolicyNode.svelte';
	import PolicyEdge from './PolicyEdge.svelte';
	import { NODE_COLORS, isFastRenderMode, applyPolicyElkLayout } from '$lib/utils/policy-layout';
	import { runQuery, clearQuery } from '$lib/stores/policy-store';
	import { themeStore } from '$lib/stores';

	interface Props {
		nodes: Node[];
		edges: Edge[];
	}

	let { nodes, edges }: Props = $props();

	const nodeTypes = {
		policyNode: PolicyNode as unknown as typeof PolicyNode
	};
	const edgeTypes = {
		policyEdge: PolicyEdge as unknown as typeof PolicyEdge
	};

	const flowNodesStore = writable<Node[]>([]);
	const flowEdgesStore = writable<Edge[]>([]);
	let isLayouting = $state(false);
	let layoutVersion = 0;

	const colorMode = $derived.by((): ColorMode => {
		const mode = $themeStore;
		if (mode === 'system') return 'system';
		if (mode === 'light') return 'light';
		return 'dark';
	});

	$effect(() => {
		const currentNodes = nodes;
		const currentEdges = edges;

		if (currentNodes.length === 0) {
			flowNodesStore.set([]);
			flowEdgesStore.set([]);
			return;
		}

		const fast = isFastRenderMode(currentNodes.length, currentEdges.length);
		if (fast) {
			const cols = Math.ceil(Math.sqrt(currentNodes.length));
			flowNodesStore.set(currentNodes.map((node, i) => ({
				...node,
				position: { x: (i % cols) * 180, y: Math.floor(i / cols) * 60 }
			})));
			flowEdgesStore.set(currentEdges);
			return;
		}

		const version = ++layoutVersion;
		isLayouting = true;
		applyPolicyElkLayout(currentNodes, currentEdges)
			.then(({ nodes: ln, edges: le }) => {
				if (version !== layoutVersion) return;
				flowNodesStore.set(ln);
				flowEdgesStore.set(le);
				// Auto-fit after layout
				requestAnimationFrame(() => {
					if (fitViewRef) fitViewRef({ duration: 400, padding: 0.15 });
				});
			})
			.finally(() => {
				if (version === layoutVersion) isLayouting = false;
			});
	});

	function handleNodeClick({ node }: { node: Node; event: MouseEvent | TouchEvent }) {
		const selector = (node.data as any)?.rawSelector ?? node.id;
		runQuery(selector, 'outbound');
	}

	function handlePaneClick() {
		clearQuery();
	}

	function miniMapNodeColor(node: Node): string {
		const nodeType = (node.data as any)?.nodeType ?? 'unknown';
		return NODE_COLORS[nodeType] ?? '#111827';
	}

	let fitViewRef: ((options?: any) => void) | null = null;

	function captureFlowInstance() {
		const { fitView } = useSvelteFlow();
		fitViewRef = fitView;
		requestAnimationFrame(() => fitView({ duration: 300, padding: 0.1 }));
	}
</script>

<div class="h-full w-full">
	{#if isLayouting}
		<div class="flex h-full items-center justify-center">
			<div class="text-muted-foreground">Calculating layout...</div>
		</div>
	{:else}
		<SvelteFlowProvider>
			<SvelteFlow
				nodes={$flowNodesStore}
				edges={$flowEdgesStore}
				{nodeTypes}
				{edgeTypes}
				{colorMode}
				fitView
				minZoom={0.01}
				maxZoom={10}
				proOptions={{ hideAttribution: true }}
				onnodeclick={handleNodeClick}
				onpaneclick={handlePaneClick}
				oninit={captureFlowInstance}
			>
				<Background />
				<Controls />
				<MiniMap
					width={120}
					height={80}
					nodeColor={miniMapNodeColor}
				/>
			</SvelteFlow>
		</SvelteFlowProvider>
	{/if}
</div>
