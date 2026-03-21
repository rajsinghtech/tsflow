<script lang="ts">
	import { NODE_COLORS, EDGE_STYLES } from '$lib/utils/policy-layout';

	const nodeLegend: { type: string; label: string }[] = [
		{ type: 'user', label: 'User' },
		{ type: 'group', label: 'Group' },
		{ type: 'tag', label: 'Tag' },
		{ type: 'autogroup', label: 'Autogroup' },
		{ type: 'host', label: 'Host' },
		{ type: 'ipset', label: 'IPset' },
		{ type: 'service', label: 'Service' },
		{ type: 'ip', label: 'IP/CIDR' }
	];

	const edgeLegend: { type: string; label: string }[] = [
		{ type: 'grant', label: 'Grant' },
		{ type: 'acl', label: 'ACL' },
		{ type: 'ssh', label: 'SSH' },
		{ type: 'member-of', label: 'Relation' }
	];
</script>

<div class="space-y-2">
	<h3 class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Legend</h3>
	<div class="grid grid-cols-2 gap-1">
		{#each nodeLegend as item}
			<div class="flex items-center gap-1.5 text-[10px]">
				<span class="inline-block h-3 w-3 rounded-sm border-2" style="border-color: {NODE_COLORS[item.type]}; background: color-mix(in srgb, {NODE_COLORS[item.type]} 20%, transparent)"></span>
				{item.label}
			</div>
		{/each}
	</div>
	<div class="space-y-0.5">
		{#each edgeLegend as item}
			{@const style = EDGE_STYLES[item.type]}
			<div class="flex items-center gap-1.5 text-[10px]">
				<svg width="24" height="8">
					<line x1="0" y1="4" x2="24" y2="4" stroke={style.color} stroke-width={style.width} stroke-dasharray={style.strokeDasharray ?? ''} opacity={style.opacity} />
				</svg>
				{item.label}
			</div>
		{/each}
	</div>
</div>
