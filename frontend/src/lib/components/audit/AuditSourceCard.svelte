<script lang="ts">
	import { auditTime, relativeTime } from '#lib/audit/format';
	import type { AuditTailnetStatus } from '#lib/audit/types';

	let { status, retention }: { status: AuditTailnetStatus; retention?: string } = $props();
	const src = $derived(status.source);
</script>

<div class="rounded-lg border border-border bg-card p-3 text-left text-xs" data-testid="audit-source-card">
	<div class="mb-2 flex items-center justify-between">
		<span class="font-medium text-foreground">Audit source · {status.displayName || status.id}</span>
		<span class="inline-flex items-center gap-1.5">
			<span class="h-2 w-2 rounded-full {src.lastError ? 'bg-amber-500' : src.running ? 'bg-green-500' : 'bg-muted-foreground/40'}"></span>
			{src.lastError ? 'failing' : src.running ? 'polling' : 'stopped'}
		</span>
	</div>
	<dl class="grid grid-cols-[8rem_minmax(0,1fr)] gap-x-3 gap-y-1">
		<dt>Backend</dt>
		<dd class="text-foreground">{src.backend}{src.bucket ? ` · ${src.bucket}/${src.prefix ?? ''}` : ''}</dd>
		{#if src.pollInterval}<dt>Polls every</dt><dd class="text-foreground">{src.pollInterval}</dd>{/if}
		<dt>Last poll</dt>
		<dd class="text-foreground" title={auditTime(src.lastPoll)}>{relativeTime(src.lastPoll)} · {src.polls} polls</dd>
		<dt>Last success</dt>
		<dd class="text-foreground" title={auditTime(src.lastSuccess)}>{src.lastSuccess ? relativeTime(src.lastSuccess) : 'not yet'}</dd>
		<dt>Stored</dt>
		<dd class="text-foreground">{status.stored.events.toLocaleString()} events{status.stored.objects ? `, ${status.stored.objects.toLocaleString()} files` : ''}{retention ? ` · kept ${retention}` : ''}</dd>
	</dl>
	{#if src.lastError}
		<div class="mt-2 text-amber-700 dark:text-amber-400">Last error {relativeTime(src.lastErrorTime)}</div>
		<pre class="mt-1 max-h-28 overflow-auto whitespace-pre-wrap break-all rounded-md bg-secondary/60 p-2 font-mono text-[11px] text-foreground">{src.lastError}</pre>
	{/if}
</div>
