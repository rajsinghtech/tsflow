<script lang="ts">
	import { Handle, Position } from '@xyflow/svelte';

	interface Props {
		data: {
			displayName: string;
			memberCount: number;
			groupKind?: string;
		};
	}

	let { data }: Props = $props();
	const kindLabel = $derived(
		data.groupKind === 'user' ? 'User' : data.groupKind === 'subnet' ? 'Subnet' : 'Tag'
	);
</script>

<div class="pointer-events-none h-full w-full rounded-lg border-2 border-dashed border-node-tailscale/70 bg-node-tailscale/5">
	<Handle type="target" position={Position.Top} class="!opacity-0" />
	<div class="px-3 pt-2 text-xs font-semibold text-node-tailscale">
		{data.displayName}
		<span class="font-normal text-muted-foreground"> · {kindLabel} · {data.memberCount} devices</span>
	</div>
	<Handle type="source" position={Position.Bottom} class="!opacity-0" />
</div>
