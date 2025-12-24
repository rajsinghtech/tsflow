<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { themeStore } from '$lib/stores/theme';
	import { networkOverviewStore, filtersStore, networkLogsStore } from '$lib/stores/data';
	import Header from '$lib/components/Header.svelte';

	interface Props {
		children: import('svelte').Snippet;
	}

	let { children }: Props = $props();

	let overview = $derived($networkOverviewStore);
	let filters = $derived($filtersStore);
	let metadata = $derived($networkLogsStore.metadata);
	let timeRangeText = $derived(filters.timeRange === 1 ? '1 min' : `${filters.timeRange} min`);

	onMount(() => {
		themeStore.init();
	});
</script>

<div class="flex flex-col h-screen">
	<Header
		nodes={overview.nodes}
		links={overview.links}
		totalTraffic={overview.totalTraffic}
		timeRange={timeRangeText}
		{metadata}
	/>
	{@render children()}
</div>
