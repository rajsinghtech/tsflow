<script lang="ts">
	import type { NetworkLink, NetworkNode } from '#lib/types';
	import { usesFullGraph } from '#lib/graph/threshold';
	import FullNetworkGraph from './FullNetworkGraph.svelte';
	import LargeNetworkGraph from './LargeNetworkGraph.svelte';

	interface Props {
		nodes: NetworkNode[];
		edges: NetworkLink[];
	}

	let { nodes, edges }: Props = $props();
</script>

{#if usesFullGraph(nodes.length)}
	<FullNetworkGraph {nodes} {edges} />
{:else}
	<LargeNetworkGraph {nodes} {edges} />
{/if}
