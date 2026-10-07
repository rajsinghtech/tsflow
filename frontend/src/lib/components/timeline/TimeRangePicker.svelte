<script lang="ts">
	import { WINDOW_PRESETS } from './time-window';
	import {
		decodeSearch,
		parseRangeInput,
		type RangeIntent,
		type RecentRange,
		type TimeZoneMode
	} from './time-range-url';

	let {
		now = new Date(),
		zone,
		recent = [],
		onCommit,
		onZone
	}: {
		now?: Date;
		zone: TimeZoneMode;
		recent?: RecentRange[];
		onCommit: (intent: RangeIntent) => void;
		onZone: (zone: TimeZoneMode) => void;
	} = $props();

	let draft = $state('');
	let error = $state('');
	let inputEl: HTMLInputElement | null = $state(null);

	$effect(() => {
		inputEl?.focus();
	});

	function submit() {
		const parsed = parseRangeInput(draft, now, zone);
		if (!parsed.ok) {
			error = parsed.message;
			return;
		}
		error = '';
		onCommit(parsed.intent);
	}

	function chooseRecent(item: RecentRange) {
		const decoded = decodeSearch(new URLSearchParams({ from: item.from, to: item.to }), now);
		if (decoded.status !== 'ok') {
			error = decoded.status === 'invalid' ? decoded.message : "Couldn't read that range.";
			return;
		}
		error = '';
		onCommit(decoded.intent);
	}
</script>

<div
	id="time-range-popover"
	role="dialog"
	aria-label="Time range"
	class="absolute top-full left-0 z-50 mt-1 w-80 max-w-[calc(100vw-1rem)] rounded-lg border border-border bg-popover p-3 text-popover-foreground shadow-xl"
>
	<form
		onsubmit={(event) => {
			event.preventDefault();
			submit();
		}}
	>
		<label class="block text-xs text-muted-foreground" for="time-range-input">Range</label>
		<input
			id="time-range-input"
			bind:this={inputEl}
			bind:value={draft}
			aria-invalid={error ? 'true' : undefined}
			aria-describedby={error ? 'time-range-error' : 'time-range-hint'}
			placeholder="2h, last 3 hours, since 9am, 14:00-15:30"
			class="mt-1 h-9 w-full rounded-md border border-input bg-background px-2 text-sm"
			autocomplete="off"
		/>
		<p id="time-range-hint" class="mt-1 text-[11px] text-muted-foreground">
			Also ISO and unix. A single start stays live.
		</p>
		{#if error}
			<p id="time-range-error" class="mt-1 text-xs text-destructive" role="alert">{error}</p>
		{/if}
		<button type="submit" class="sr-only">Apply range</button>
	</form>

	<div class="mt-3 grid grid-cols-4 gap-1" role="group" aria-label="Presets">
		{#each WINDOW_PRESETS as preset}
			<button
				type="button"
				class="min-h-8 rounded-md border border-border px-2 text-xs hover:bg-secondary focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)]"
				onclick={() => onCommit({ kind: 'sliding', windowMs: preset.ms })}
			>
				{preset.label}
			</button>
		{/each}
		<button
			type="button"
			class="min-h-8 rounded-md border border-border px-2 text-xs hover:bg-secondary focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-ring)]"
			onclick={() => onCommit({ kind: 'all' })}
		>
			All
		</button>
	</div>

	{#if recent.length > 0}
		<div class="mt-3">
			<div class="text-xs text-muted-foreground">Recent</div>
			<div class="mt-1 flex flex-col gap-1">
				{#each recent as item}
					<button
						type="button"
						class="min-h-7 truncate rounded-md px-2 text-left text-xs hover:bg-secondary"
						onclick={() => chooseRecent(item)}
					>
						{item.label}
					</button>
				{/each}
			</div>
		</div>
	{/if}

	<div class="mt-3 flex items-center justify-between">
		<span class="text-xs text-muted-foreground" id="time-zone-label">Time zone</span>
		<div class="flex gap-1" role="group" aria-labelledby="time-zone-label">
			<button
				type="button"
				aria-pressed={zone === 'local'}
				class="min-h-7 rounded-md border border-border px-2 text-xs hover:bg-secondary"
				class:bg-secondary={zone === 'local'}
				class:border-primary={zone === 'local'}
				onclick={() => onZone('local')}
			>
				Local
			</button>
			<button
				type="button"
				aria-pressed={zone === 'utc'}
				class="min-h-7 rounded-md border border-border px-2 text-xs hover:bg-secondary"
				class:bg-secondary={zone === 'utc'}
				class:border-primary={zone === 'utc'}
				onclick={() => onZone('utc')}
			>
				UTC
			</button>
		</div>
	</div>
</div>
