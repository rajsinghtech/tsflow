<script lang="ts">
	import '../app.css';
	import '@xyflow/svelte/dist/style.css';

	let { children } = $props();
</script>

<div class="min-h-screen bg-background text-foreground">
	{@render children()}
</div>
