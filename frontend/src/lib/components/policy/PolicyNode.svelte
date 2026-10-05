<script lang="ts">
	import { Handle, Position } from '@xyflow/svelte';
	import { highlightedPolicyNodeIds, hasQuery } from '#lib/stores/policy-store';
	import type { NodeType } from '#lib/policy-engine/types';
	import type { SelfAccessSummary } from '#lib/utils/self-access';
	import SelfAccessBadge from './SelfAccessBadge.svelte';

	interface Props {
		data: {
			label: string;
			nodeType: NodeType;
			rawSelector?: string;
			color: string;
			selfAccess?: SelfAccessSummary;
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
	class="min-w-[60px] border-2 px-3 py-1.5 text-xs font-medium transition-opacity duration-200 select-none {data.selfAccess ? 'max-w-[220px]' : 'max-w-[200px] overflow-hidden'}"
	class:opacity-20={isDimmed}
	class:ring-2={isHighlighted && $hasQuery}
	class:ring-white={isHighlighted && $hasQuery}
	style="
		border-color: {data.color};
		background: color-mix(in srgb, {data.color} 15%, var(--color-card));
		color: {data.color};
		border-radius: {borderRadius};
	"
	title={data.selfAccess ? undefined : (data.rawSelector ?? data.label)}
>
	<Handle type="target" position={Position.Top} class="!opacity-0" />
	{#if data.selfAccess}
		<div class="flex items-center gap-1.5">
			<span class="min-w-0 truncate" title={data.rawSelector ?? data.label}>{data.label}</span>
			<SelfAccessBadge
				color={data.color}
				tooltip={data.selfAccess.tooltip}
				ariaLabel={data.selfAccess.ariaLabel}
			/>
		</div>
	{:else}
		<span class="block truncate">{data.label}</span>
	{/if}
	<Handle type="source" position={Position.Bottom} class="!opacity-0" />
</div>
