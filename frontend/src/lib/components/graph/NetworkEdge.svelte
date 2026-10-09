<script lang="ts">
	import { BaseEdge, getBezierPath } from '@xyflow/svelte';
	import type { NetworkLink } from '#lib/types';
	import { hasSelection } from '#lib/stores/ui-store';
	import { highlightedNodeIds } from '#lib/stores/selection-highlight';

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
		data?: NetworkLink;
	}

	let { id, source, target, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, data }: Props =
		$props();

	// Check if this edge connects highlighted nodes
	const isHighlighted = $derived(
		$highlightedNodeIds.has(source) && $highlightedNodeIds.has(target)
	);
	const isDimmed = $derived($hasSelection && !isHighlighted);

	// Calculate edge path (curved bezier)
	const edgePath = $derived(
		getBezierPath({
			sourceX,
			sourceY,
			sourcePosition,
			targetX,
			targetY,
			targetPosition
		})[0]
	);

	// Edge color based on traffic type
	const edgeColor = $derived.by(() => {
		if (!data) return 'var(--color-muted-foreground)';
		switch (data.trafficType) {
			case 'virtual':
				return 'var(--color-traffic-virtual)';
			case 'subnet':
				return 'var(--color-traffic-subnet)';
			case 'physical':
				return 'var(--color-traffic-physical)';
			default:
				return 'var(--color-muted-foreground)';
		}
	});

	// Edge width based on traffic volume
	const edgeWidth = $derived.by(() => {
		if (!data) return 1;
		const bytes = data.totalBytes;
		if (bytes > 10000000) return 4;
		if (bytes > 1000000) return 3;
		if (bytes > 100000) return 2;
		return 1;
	});
</script>

<BaseEdge
	{id}
	path={edgePath}
	style="stroke: {edgeColor}; stroke-width: {edgeWidth}px; opacity: {isDimmed ? 0.15 : 1}; transition: opacity 0.2s;"
/>
