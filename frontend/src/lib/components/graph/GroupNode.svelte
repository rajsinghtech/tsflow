<script lang="ts">
	import { Handle, Position } from '@xyflow/svelte';
	import { Server } from 'lucide-svelte';
	import { formatBytes } from '#lib/utils';

	interface Props {
		data: {
			id: string;
			displayName: string;
			totalBytes: number;
			connections: number;
			user?: string;
			groupKind?: string;
			memberCount: number;
		};
	}

	let { data }: Props = $props();

	const nodeColor = 'var(--color-node-tailscale)';
	const kindLabel = $derived(
		data.groupKind === 'user' ? 'User' : data.groupKind === 'subnet' ? 'Subnet' : 'Tag'
	);
</script>

<div
	class="min-w-[180px] w-fit rounded-lg border-2 bg-card shadow-lg shadow-md shadow-black/10"
	style="border-color: {nodeColor}"
>
	<Handle type="target" position={Position.Top} class="!opacity-0" />

	<div class="rounded-t-md px-3 py-2" style="background: color-mix(in srgb, {nodeColor} 15%, var(--color-card))">
		<div class="flex items-start justify-between gap-3">
			<div class="flex min-w-0 items-center gap-2">
				<Server class="h-4 w-4 shrink-0" style="color: {nodeColor}" />
				<span class="text-sm font-semibold leading-tight" style="color: {nodeColor}" title={data.displayName}>
					{data.displayName}
				</span>
			</div>
			<div class="shrink-0 whitespace-nowrap text-right">
				<div class="text-xs font-bold text-node-private">{formatBytes(data.totalBytes)}</div>
				<div class="text-xs text-muted-foreground">{data.connections} conn</div>
			</div>
		</div>
		{#if data.user}
			<div class="mt-1 text-xs text-muted-foreground">
				<span class="opacity-70">Group:</span>
				{data.user}
			</div>
		{/if}
	</div>

	<div class="px-3 py-2 text-xs text-muted-foreground">Click to expand</div>

	<div class="flex items-center justify-between border-t border-border px-3 py-1.5">
		<div class="flex items-center gap-1.5">
			<div class="h-2 w-2 rounded-full bg-node-tailscale"></div>
			<span class="text-xs text-node-tailscale">{kindLabel}</span>
		</div>
		<span class="text-xs text-muted-foreground">{data.memberCount} devices</span>
	</div>

	<Handle type="source" position={Position.Bottom} class="!opacity-0" />
</div>
