<script lang="ts">
	import '../app.css';
	import '@xyflow/svelte/dist/style.css';
	import { onMount } from 'svelte';
	import { get } from 'svelte/store';
	import { policyGraph, fetchAndRenderPolicy } from '$lib/stores/policy-store';

	let { children } = $props();

	// Auto-fetch policy on app startup so it's available on all pages
	onMount(() => {
		if (!get(policyGraph)) {
			fetchAndRenderPolicy();
		}
	});
</script>

<div class="min-h-screen bg-background text-foreground">
	{@render children()}
</div>
