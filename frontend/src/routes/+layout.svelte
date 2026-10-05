<script lang="ts">
	import { onMount } from 'svelte';
	import '../app.css';
	import '@xyflow/svelte/dist/style.css';
	import { loadViewerIdentity } from '#lib/stores/viewer-store';

	let { children } = $props();

	onMount(() => {
		loadViewerIdentity();
	});
</script>

<div class="min-h-screen bg-background text-foreground">
	{@render children()}
</div>
