<script lang="ts">
	import { X } from 'lucide-svelte';
	import { filterStore, uiStore } from '$lib/stores';
	import type { TrafficType } from '$lib/types';
	import TimelineSlider from '$lib/components/timeline/TimelineSlider.svelte';

	// Traffic type options including exit node traffic
	const trafficTypes: { value: TrafficType; label: string; defaultOn: boolean }[] = [
		{ value: 'virtual', label: 'Virtual', defaultOn: true },
		{ value: 'exit', label: 'Exit Node', defaultOn: false },
		{ value: 'subnet', label: 'Subnet', defaultOn: true },
		{ value: 'physical', label: 'Physical', defaultOn: false }
	];

	const selectedTrafficTypes = $derived(new Set($filterStore.trafficTypes));

	function toggleTrafficType(type: string) {
		const next = new Set($filterStore.trafficTypes);
		if (next.has(type as TrafficType)) {
			next.delete(type as TrafficType);
		} else {
			next.add(type as TrafficType);
		}
		filterStore.setTrafficTypes([...next]);
	}

	function selectAllTrafficTypes() {
		filterStore.setTrafficTypes(trafficTypes.map((t) => t.value));
	}

	function clearAllTrafficTypes() {
		filterStore.setTrafficTypes([]);
	}

	// Get color for traffic type indicator
	function getTrafficTypeColor(type: string): string {
		switch (type) {
			case 'virtual':
				return 'bg-blue-500';
			case 'exit':
				return 'bg-purple-500';
			case 'subnet':
				return 'bg-green-500';
			case 'physical':
				return 'bg-amber-500';
			default:
				return 'bg-gray-500';
		}
	}
</script>

<div class="flex h-full flex-col overflow-y-auto p-4">
	<div class="mb-4 flex items-center justify-between">
		<h2 class="text-lg font-semibold">Filters</h2>
		<button
			type="button"
			class="flex h-8 w-8 items-center justify-center rounded-md border border-transparent text-muted-foreground hover:border-border hover:bg-secondary hover:text-foreground"
			onclick={() => uiStore.toggleFilters()}
			title="Close filters"
			aria-label="Close filters"
		>
			<X class="h-4 w-4" />
		</button>
	</div>

	<!-- Search -->
	<div class="mb-4">
		<label for="search-input" class="mb-1 block text-sm font-medium">Search</label>
		<div class="relative">
			<input
				id="search-input"
				type="text"
				placeholder="Search devices, tag:k8s, ip:100..."
				class="w-full rounded-md border border-input bg-background py-2 pl-3 pr-8 text-sm"
				value={$filterStore.search}
				oninput={(e) => filterStore.setSearch(e.currentTarget.value)}
			/>
			{#if $filterStore.search}
				<button
					onclick={() => filterStore.setSearch('')}
					class="absolute right-1.5 top-1/2 flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground hover:bg-secondary hover:text-foreground"
					title="Clear search"
					aria-label="Clear search"
				>
					<X class="h-4 w-4" />
				</button>
			{/if}
		</div>
		<ul class="mt-1 space-y-0.5 text-xs text-muted-foreground">
			<li>• <span class="text-primary">tag:k8s</span> - Find devices with specific tags</li>
			<li>• <span class="text-primary">ip:100.88</span> - Find devices by IP address</li>
			<li>• <span class="text-primary">user@github</span> - Find devices by user</li>
			<li>• Regular text searches device names, IPs, and tags</li>
		</ul>
	</div>

	<!-- Traffic Type -->
	<fieldset class="mb-4">
		<legend class="mb-1 block text-sm font-medium">Traffic Type</legend>
		<div class="space-y-1">
			{#each trafficTypes as type}
				<label class="flex items-center gap-2 text-sm">
					<input
						type="checkbox"
						class="rounded border-input"
						checked={selectedTrafficTypes.has(type.value)}
						onchange={() => toggleTrafficType(type.value)}
					/>
					<span class="flex items-center gap-1">
						<span class="h-2 w-2 rounded-full {getTrafficTypeColor(type.value)}"></span>
						{type.label}
					</span>
				</label>
			{/each}
		</div>
		<div class="mt-2 flex gap-2 text-xs">
			<button onclick={selectAllTrafficTypes} class="rounded-md border border-border px-2 py-1 hover:bg-secondary">All</button
			>
			<button onclick={clearAllTrafficTypes} class="rounded-md border border-border px-2 py-1 hover:bg-secondary">
				None ({selectedTrafficTypes.size})
			</button>
		</div>
	</fieldset>

	<!-- Time Window -->
	<div class="mb-4 border-t border-border pt-4">
		<TimelineSlider />
	</div>
</div>
