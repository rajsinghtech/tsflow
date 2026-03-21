<script lang="ts">
	import { BaseEdge, getBezierPath } from '@xyflow/svelte';
	import { highlightedPolicyEdgeIds, hasQuery } from '$lib/stores/policy-store';

	interface Props {
		id: string;
		source: string;
		target: string;
		sourceX: number;
		sourceY: number;
		targetX: number;
		targetY: number;
		sourcePosition: any;
		targetPosition: any;
		data?: {
			edgeType: string;
			color: string;
			strokeDasharray?: string;
			width: number;
			opacity: number;
			meta?: any;
		};
	}

	let { id, source, target, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, data }: Props =
		$props();

	const isHighlighted = $derived($highlightedPolicyEdgeIds.has(id));
	const isDimmed = $derived($hasQuery && !isHighlighted);

	const pathResult = $derived(
		getBezierPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition })
	);
	const edgePath = $derived(pathResult[0]);

	const color = $derived(data?.color ?? '#9ca3af');
	const width = $derived(data?.width ?? 1);
	const dasharray = $derived(data?.strokeDasharray ?? '');
	const baseOpacity = $derived(data?.opacity ?? 1);
	const markerId = $derived(`arrow-${id.replace(/[^a-zA-Z0-9]/g, '-')}`);
	const isRelation = $derived(data?.edgeType === 'member-of' || data?.edgeType === 'owns-tag' || data?.edgeType === 'contains' || data?.edgeType === 'resolves-to');

</script>

<!-- Arrow marker definition -->
<svg style="position: absolute; width: 0; height: 0;">
	<defs>
		<marker
			id={markerId}
			viewBox="0 0 10 10"
			refX="8"
			refY="5"
			markerWidth="6"
			markerHeight="6"
			orient="auto-start-reverse"
		>
			<path d="M 0 0 L 10 5 L 0 10 z" fill={color} opacity={isDimmed ? 0.1 : isHighlighted && $hasQuery ? 1 : baseOpacity} />
		</marker>
	</defs>
</svg>

<BaseEdge
	{id}
	path={edgePath}
	style="stroke: {color}; stroke-width: {isHighlighted && $hasQuery ? width + 1 : width}px; stroke-dasharray: {dasharray}; opacity: {isDimmed ? 0.1 : isHighlighted && $hasQuery ? 1 : baseOpacity}; transition: opacity 0.2s, stroke-width 0.2s; {isRelation ? '' : `marker-end: url(#${markerId});`}"
/>

