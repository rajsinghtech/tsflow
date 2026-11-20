<script lang="ts">
	import { ScrollText, ShieldOff } from 'lucide-svelte';

	let { reason }: { reason: 'disabled' | 'forbidden' | 'error' } = $props();
</script>

<div class="mx-auto mt-10 max-w-lg rounded-lg border border-border bg-card p-6 text-sm">
	{#if reason === 'disabled'}
		<div class="mb-3 flex items-center gap-2 text-base font-semibold"><ScrollText class="h-5 w-5 text-muted-foreground" /> Audit logs are off</div>
		<p class="mb-3 text-muted-foreground">
			tsflow can keep a searchable copy of Tailscale's configuration audit log: logins, device approvals, tag and route changes, policy file
			edits, keys and settings.
		</p>
		<p class="mb-2 text-muted-foreground">To turn it on, set:</p>
		<pre class="mb-3 overflow-x-auto rounded-md bg-secondary/60 p-3 font-mono text-xs">TSFLOW_AUDIT_ENABLED=true
TSFLOW_AUDIT_VIEWERS=you@example.com,group:security
# where the logs come from: gcs, s3 or api
TSFLOW_AUDIT_BACKEND=gcs
TSFLOW_AUDIT_BUCKET=example-audit-logs</pre>
		<p class="text-xs text-muted-foreground">See docs/audit-logs.md for streaming setup and per-tailnet settings.</p>
	{:else if reason === 'forbidden'}
		<div class="mb-3 flex items-center gap-2 text-base font-semibold"><ShieldOff class="h-5 w-5 text-muted-foreground" /> No access to audit logs</div>
		<p class="text-muted-foreground">
			Audit logs are on, but your login isn't in the audit viewers list. Ask whoever runs tsflow to add you or your group to
			<code class="font-mono text-xs">TSFLOW_AUDIT_VIEWERS</code>.
		</p>
	{:else}
		<div class="mb-3 text-base font-semibold">Couldn't load audit logs</div>
		<p class="text-muted-foreground">Try again in a moment.</p>
	{/if}
</div>
