<script lang="ts">
	import { Handle, Position } from '@xyflow/svelte';
	import { highlightedPolicyNodeIds, hasQuery } from '$lib/stores/policy-store';
	import type { NodeType } from '$lib/policy-engine/types';

	interface Props {
		data: {
			label: string;
			nodeType: NodeType;
			rawSelector?: string;
			color: string;
		};
	}

	let { data }: Props = $props();

	const isHighlighted = $derived($highlightedPolicyNodeIds.has(data.rawSelector ?? data.label));
	const isDimmed = $derived($hasQuery && !isHighlighted);

	const borderRadius = $derived.by(() => {
		switch (data.nodeType) {
			case 'user': return '9999px';
			case 'group':
			case 'ipset': return '8px';
			case 'tag': return '0';
			case 'host': return '4px';
			default: return '8px';
		}
	});
</script>

<div
	class="max-w-[200px] min-w-[60px] overflow-hidden border-2 px-3 py-1.5 text-xs font-medium transition-opacity duration-200 select-none"
	class:opacity-20={isDimmed}
	class:ring-2={isHighlighted && $hasQuery}
	class:ring-white={isHighlighted && $hasQuery}
	style="
		border-color: {data.color};
		background: color-mix(in srgb, {data.color} 15%, var(--color-card));
		color: {data.color};
		border-radius: {borderRadius};
	"
	title={data.rawSelector ?? data.label}
>
	<Handle type="target" position={Position.Top} class="!opacity-0" />
	<span class="block truncate">{data.label}</span>
	<Handle type="source" position={Position.Bottom} class="!opacity-0" />
</div>
