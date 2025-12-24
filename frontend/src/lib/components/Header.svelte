<script lang="ts">
	import { Sun, Moon, Monitor, Check, Activity, Link2, HardDrive, AlertCircle, Clock } from 'lucide-svelte';
	import { themeStore } from '$lib/stores/theme';
	import { formatBytes } from '$lib/utils/format';
	import { formatDistanceToNow } from 'date-fns';

	interface Props {
		nodes?: any[];
		links?: any[];
		totalTraffic?: number;
		timeRange?: string;
		metadata?: {
			sampled?: boolean;
			totalLogs?: number;
			lastUpdated?: string;
			chunked?: boolean;
		};
	}

	let { nodes = [], links = [], totalTraffic = 0, timeRange = '', metadata = {} }: Props = $props();

	function getLastUpdatedText(timestamp?: string): string {
		if (!timestamp) return '';
		try {
			return formatDistanceToNow(new Date(timestamp), { addSuffix: true });
		} catch {
			return '';
		}
	}

	let themeDropdownVisible = $state(false);

	let currentTheme = $derived($themeStore);

	function toggleThemeDropdown() {
		themeDropdownVisible = !themeDropdownVisible;
	}

	function setTheme(theme: 'light' | 'dark' | 'system') {
		themeStore.set(theme);
		themeDropdownVisible = false;
	}

	function closeDropdowns(e: MouseEvent) {
		const target = e.target as HTMLElement;
		if (
			!target.closest('.theme-dropdown-container') &&
			!target.closest('.theme-button')
		) {
			themeDropdownVisible = false;
		}
	}

	$effect(() => {
		if (typeof window !== 'undefined') {
			window.addEventListener('click', closeDropdowns);
			return () => window.removeEventListener('click', closeDropdowns);
		}
	});
</script>

<header style="background: var(--color-bg-surface); border-bottom: 1px solid var(--color-border-base);">
	<nav class="max-w-full mx-auto px-6 py-3">
		<div class="flex items-center justify-between">
			<div class="flex items-center gap-8">
				<a href="/" class="flex items-center gap-3 group">
					<svg
						width="20"
						height="20"
						viewBox="0 0 23 23"
						fill="none"
						xmlns="http://www.w3.org/2000/svg"
						aria-hidden="true"
						class="transition-transform group-hover:scale-105"
						style="color: var(--color-text-primary);"
					>
						<circle opacity="0.3" cx="3.4" cy="3.25" r="2.7" fill="currentColor" />
						<circle cx="3.4" cy="11.3" r="2.7" fill="currentColor" />
						<circle opacity="0.3" cx="3.4" cy="19.5" r="2.7" fill="currentColor" />
						<circle cx="11.5" cy="11.3" r="2.7" fill="currentColor" />
						<circle cx="11.5" cy="19.5" r="2.7" fill="currentColor" />
						<circle opacity="0.3" cx="11.5" cy="3.25" r="2.7" fill="currentColor" />
						<circle opacity="0.3" cx="19.5" cy="3.25" r="2.7" fill="currentColor" />
						<circle cx="19.5" cy="11.3" r="2.7" fill="currentColor" />
						<circle opacity="0.3" cx="19.5" cy="19.5" r="2.7" fill="currentColor" />
					</svg>
					<h1 class="text-lg font-semibold" style="color: var(--color-text-base);">TSFlow</h1>
				</a>

				{#if nodes.length > 0 || links.length > 0}
					<div class="flex items-center gap-6 text-sm">
						<div class="flex items-center gap-2">
							<Activity class="w-4 h-4" style="color: var(--color-text-primary);" />
							<span style="color: var(--color-text-muted);">Nodes:</span>
							<span class="font-semibold" style="color: var(--color-text-base);">{nodes.length}</span>
						</div>
						<div class="flex items-center gap-2">
							<Link2 class="w-4 h-4" style="color: var(--color-text-primary);" />
							<span style="color: var(--color-text-muted);">Flows:</span>
							<span class="font-semibold" style="color: var(--color-text-base);">{links.length}</span>
						</div>
						<div class="flex items-center gap-2">
							<HardDrive class="w-4 h-4" style="color: var(--color-text-primary);" />
							<span style="color: var(--color-text-muted);">Traffic:</span>
							<span class="font-semibold" style="color: var(--color-text-base);">{formatBytes(totalTraffic)}</span>
						</div>
						{#if timeRange}
							<div class="flex items-center gap-2 px-2 py-1 rounded" style="background: var(--color-bg-interactive);">
								<div class="w-1.5 h-1.5 bg-green-500 rounded-full animate-pulse"></div>
								<span class="text-xs" style="color: var(--color-text-muted);">Last {timeRange}</span>
							</div>
						{/if}
						{#if metadata?.sampled}
							<div
								class="flex items-center gap-1.5 px-2 py-1 rounded"
								style="background: rgba(245, 158, 11, 0.1); border: 1px solid rgba(245, 158, 11, 0.3);"
								title="Data was sampled due to large query size"
							>
								<AlertCircle class="w-3.5 h-3.5" style="color: rgb(245, 158, 11);" />
								<span class="text-xs font-medium" style="color: rgb(245, 158, 11);">Sampled</span>
							</div>
						{/if}
						{#if metadata?.lastUpdated}
							<div class="flex items-center gap-1.5 px-2 py-1 rounded" style="background: var(--color-bg-interactive);">
								<Clock class="w-3.5 h-3.5" style="color: var(--color-text-muted);" />
								<span class="text-xs" style="color: var(--color-text-muted);">{getLastUpdatedText(metadata.lastUpdated)}</span>
							</div>
						{/if}
					</div>
				{/if}
			</div>

			<div class="theme-dropdown-container relative">
				<button
					class="theme-button w-9 h-9 rounded-lg border-0 p-0 cursor-pointer transition-all hover:bg-opacity-80"
					style="background: var(--color-bg-interactive);"
					onclick={toggleThemeDropdown}
					title="Change theme"
				>
					<div class="w-full h-full flex items-center justify-center">
						{#if currentTheme === 'light'}
							<Sun class="w-5 h-5" style="color: var(--color-text-base);" />
						{:else if currentTheme === 'dark'}
							<Moon class="w-5 h-5" style="color: var(--color-text-base);" />
						{:else}
							<Monitor class="w-5 h-5" style="color: var(--color-text-base);" />
						{/if}
					</div>
				</button>

				{#if themeDropdownVisible}
					<div
						class="theme-dropdown absolute top-full right-0 rounded-lg mt-2 flex flex-col min-w-[160px] z-20 overflow-hidden"
						style="background: var(--color-bg-surface); border: 1px solid var(--color-border-base); box-shadow: var(--shadow-popover);"
					>
						<button
							onclick={() => setTheme('light')}
							class="flex items-center gap-3 px-4 py-2.5 cursor-pointer w-full text-left border-0 transition-colors text-sm"
							style="background: transparent; color: var(--color-text-base);"
							onmouseenter={(e) => e.currentTarget.style.background = 'var(--color-bg-menu-item-hover)'}
							onmouseleave={(e) => e.currentTarget.style.background = 'transparent'}
						>
							<Sun class="w-4 h-4" />
							<span class="flex-1">Light</span>
							{#if currentTheme === 'light'}
								<Check class="w-4 h-4" style="color: var(--color-text-primary);" />
							{/if}
						</button>
						<button
							onclick={() => setTheme('dark')}
							class="flex items-center gap-3 px-4 py-2.5 cursor-pointer w-full text-left border-0 transition-colors text-sm"
							style="background: transparent; color: var(--color-text-base);"
							onmouseenter={(e) => e.currentTarget.style.background = 'var(--color-bg-menu-item-hover)'}
							onmouseleave={(e) => e.currentTarget.style.background = 'transparent'}
						>
							<Moon class="w-4 h-4" />
							<span class="flex-1">Dark</span>
							{#if currentTheme === 'dark'}
								<Check class="w-4 h-4" style="color: var(--color-text-primary);" />
							{/if}
						</button>
						<button
							onclick={() => setTheme('system')}
							class="flex items-center gap-3 px-4 py-2.5 cursor-pointer w-full text-left border-0 transition-colors text-sm"
							style="background: transparent; color: var(--color-text-base);"
							onmouseenter={(e) => e.currentTarget.style.background = 'var(--color-bg-menu-item-hover)'}
							onmouseleave={(e) => e.currentTarget.style.background = 'transparent'}
						>
							<Monitor class="w-4 h-4" />
							<span class="flex-1">System</span>
							{#if currentTheme === 'system'}
								<Check class="w-4 h-4" style="color: var(--color-text-primary);" />
							{/if}
						</button>
					</div>
				{/if}
			</div>
		</div>
	</nav>
</header>
