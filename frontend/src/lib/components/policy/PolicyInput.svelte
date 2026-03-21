<script lang="ts">
	import { FileText, RefreshCw, Loader2 } from 'lucide-svelte';
	import { renderPolicy, isParsing, policyText, fetchAndRenderPolicy, fetchError } from '$lib/stores/policy-store';

	let textInput = $state($policyText);
	let mode = $state<'fetch' | 'paste'>('fetch');

	// Keep local state in sync when store updates from fetch
	policyText.subscribe((val) => {
		if (val && val !== textInput) textInput = val;
	});

	function handleRender() {
		renderPolicy(textInput);
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
			handleRender();
		}
	}

	const EXAMPLE_POLICY = `{
  "groups": {
    "group:eng": ["alice@example.com", "bob@example.com"]
  },
  "tagOwners": {
    "tag:server": ["group:eng"]
  },
  "grants": [
    {
      "src": ["group:eng"],
      "dst": ["tag:server"],
      "ip": ["tcp:443", "tcp:8080"]
    }
  ],
  "acls": [
    {
      "action": "accept",
      "src": ["group:eng"],
      "dst": ["tag:server:22"]
    }
  ]
}`;

	function loadExample() {
		textInput = EXAMPLE_POLICY;
		renderPolicy(EXAMPLE_POLICY);
	}
</script>

<div class="space-y-2">
	<div class="flex items-center justify-between">
		<h3 class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Policy Source</h3>
		<div class="flex gap-2">
			<button onclick={loadExample} class="text-xs text-primary hover:underline">Example</button>
		</div>
	</div>

	<!-- Mode toggle -->
	<div class="flex gap-1">
		<button
			class="flex-1 rounded-md px-2 py-1 text-xs font-medium transition-colors {mode === 'fetch' ? 'bg-primary/20 text-primary border border-primary' : 'border border-border text-muted-foreground hover:bg-secondary'}"
			onclick={() => (mode = 'fetch')}
		>
			Fetch API
		</button>
		<button
			class="flex-1 rounded-md px-2 py-1 text-xs font-medium transition-colors {mode === 'paste' ? 'bg-primary/20 text-primary border border-primary' : 'border border-border text-muted-foreground hover:bg-secondary'}"
			onclick={() => (mode = 'paste')}
		>
			Paste JSON
		</button>
	</div>

	{#if mode === 'fetch'}
		<button
			onclick={fetchAndRenderPolicy}
			disabled={$isParsing}
			class="flex w-full items-center justify-center gap-2 rounded-md bg-secondary px-3 py-1.5 text-sm font-medium text-secondary-foreground hover:bg-secondary/80 disabled:opacity-50"
		>
			{#if $isParsing}
				<Loader2 class="h-4 w-4 animate-spin" />
				Fetching...
			{:else}
				<RefreshCw class="h-4 w-4" />
				Refresh Policy
			{/if}
		</button>
		{#if $fetchError}
			<p class="text-xs text-destructive">{$fetchError}</p>
		{/if}
		{#if textInput}
			<details class="text-xs">
				<summary class="cursor-pointer text-muted-foreground hover:text-foreground">View raw JSON</summary>
				<pre class="mt-1 max-h-40 overflow-auto rounded-md border border-input bg-background p-2 font-mono text-[10px]">{textInput}</pre>
			</details>
		{/if}
	{:else}
		<textarea
			class="w-full resize-y rounded-md border border-input bg-background p-2 font-mono text-xs"
			rows="8"
			placeholder='Paste Tailscale policy JSON...'
			bind:value={textInput}
			onkeydown={handleKeydown}
		></textarea>
		<button
			onclick={handleRender}
			disabled={$isParsing || !textInput.trim()}
			class="flex w-full items-center justify-center gap-2 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
		>
			<FileText class="h-4 w-4" />
			Render Graph
		</button>
	{/if}
</div>
