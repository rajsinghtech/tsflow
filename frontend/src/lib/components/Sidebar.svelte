<script lang="ts">
	import { filtersStore, networkLogsStore, devicesStore } from '$lib/stores/data';
	import { onMount } from 'svelte';
	import { ChevronLeft, RefreshCw } from 'lucide-svelte';

	interface Props {
		sidebarVisible: boolean;
	}

	let { sidebarVisible = $bindable() }: Props = $props();

	const filters = $derived($filtersStore);

	const trafficTypes = [
		{ value: 'virtual', label: 'Virtual' },
		{ value: 'subnet', label: 'Subnet' },
		{ value: 'exit', label: 'Exit' },
		{ value: 'physical', label: 'Physical' }
	];

	let searchQuery = $state('');
	let selectedTrafficTypes = $derived(filters.trafficTypes);
	let timeRangePreset = $state('5m');
	let useCustomTimeRange = $state(false);
	let startDate = $state('');
	let endDate = $state('');

	let isRefreshing = $state(false);

	async function handleRefresh() {
		isRefreshing = true;
		try {
			if (useCustomTimeRange && startDate && endDate) {
				const start = new Date(startDate).toISOString();
				const end = new Date(endDate).toISOString();
				await networkLogsStore.fetchLogs(start, end);
			} else {
				const minutes = parseTimeRangeToMinutes(timeRangePreset);
				await networkLogsStore.fetchLogs(minutes);
			}
			await devicesStore.fetchDevices();
		} finally {
			isRefreshing = false;
		}
	}

	function parseTimeRangeToMinutes(preset: string): number {
		switch (preset) {
			case '1m':
				return 1;
			case '5m':
				return 5;
			case '15m':
				return 15;
			case '30m':
				return 30;
			case '1h':
				return 60;
			case '6h':
				return 360;
			case '24h':
				return 1440;
			case '7d':
				return 10080;
			case '30d':
				return 43200;
			default:
				return 5;
		}
	}

	function handleTrafficTypeToggle(type: string) {
		if (selectedTrafficTypes.includes(type)) {
			// Prevent removing the last traffic type
			if (selectedTrafficTypes.length > 1) {
				selectedTrafficTypes = selectedTrafficTypes.filter((t) => t !== type);
			}
		} else {
			selectedTrafficTypes = [...selectedTrafficTypes, type];
		}
		filtersStore.updateTrafficTypes(selectedTrafficTypes);
	}

	function selectAllTrafficTypes() {
		selectedTrafficTypes = trafficTypes.map((t) => t.value);
		filtersStore.updateTrafficTypes(selectedTrafficTypes);
	}

	function clearAllTrafficTypes() {
		// Prevent clearing all traffic types - keep at least virtual
		selectedTrafficTypes = ['virtual'];
		filtersStore.updateTrafficTypes(selectedTrafficTypes);
	}

	$effect(() => {
		if (!useCustomTimeRange) {
			const minutes = parseTimeRangeToMinutes(timeRangePreset);
			filtersStore.updateTimeRange(minutes);
		}
	});

	onMount(() => {
		const now = new Date();
		const fiveMinutesAgo = new Date(now.getTime() - 5 * 60 * 1000);

		const formatForInput = (date: Date) => {
			const year = date.getFullYear();
			const month = String(date.getMonth() + 1).padStart(2, '0');
			const day = String(date.getDate()).padStart(2, '0');
			const hours = String(date.getHours()).padStart(2, '0');
			const minutes = String(date.getMinutes()).padStart(2, '0');
			return `${year}-${month}-${day}T${hours}:${minutes}`;
		};

		startDate = formatForInput(fiveMinutesAgo);
		endDate = formatForInput(now);
	});
</script>

<div class="flex flex-col h-full" style="background: var(--color-bg-surface);">
	<!-- Header -->
	<div class="flex items-center justify-between px-4 py-3 border-b" style="border-color: var(--color-border-base);">
		<h3 class="text-base font-semibold" style="color: var(--color-text-base);">Filters</h3>
		<button
			onclick={() => sidebarVisible = false}
			class="p-1.5 rounded-md transition-colors"
			style="color: var(--color-text-muted); hover:background-color: var(--color-bg-interactive);"
			title="Hide filters"
		>
			<ChevronLeft class="w-5 h-5" />
		</button>
	</div>

	<!-- Scrollable Content -->
	<div class="flex-1 overflow-y-auto px-4 py-4 space-y-4">
		<!-- Search -->
		<div>
			<label for="search-input" class="block text-sm font-medium mb-2" style="color: var(--color-text-base);">Search</label>
			<input
				id="search-input"
				type="text"
				bind:value={searchQuery}
				oninput={() => filtersStore.updateSearch(searchQuery)}
				placeholder="Search devices, tags, IPs..."
				class="input w-full text-sm"
			/>
			<div class="mt-2 text-xs space-y-1" style="color: var(--color-text-muted);">
				<div><code class="px-1.5 py-0.5 rounded text-xs" style="background: var(--color-bg-interactive); color: var(--color-text-base);">tag:k8s</code> - Find devices with tags</div>
				<div><code class="px-1.5 py-0.5 rounded text-xs" style="background: var(--color-bg-interactive); color: var(--color-text-base);">ip:100.88</code> - Find by IP address</div>
				<div><code class="px-1.5 py-0.5 rounded text-xs" style="background: var(--color-bg-interactive); color: var(--color-text-base);">user@example.com</code> - Find by user</div>
			</div>
		</div>

		<!-- Time Range -->
		<div>
			<div class="block text-sm font-medium mb-2" style="color: var(--color-text-base);">Time Range</div>

			<!-- Custom time range toggle -->
			<div class="flex items-center gap-2 mb-3">
				<input
					type="checkbox"
					id="customTimeRange"
					bind:checked={useCustomTimeRange}
					class="rounded"
					style="border-color: var(--color-border-interactive);"
				/>
				<label for="customTimeRange" class="text-sm cursor-pointer" style="color: var(--color-text-base);">
					Custom Date Range
				</label>
			</div>

			{#if useCustomTimeRange}
				<div class="space-y-2.5">
					<div>
						<label for="start-date" class="block text-xs mb-1" style="color: var(--color-text-muted);">
							Start Date & Time
						</label>
						<input
							id="start-date"
							type="datetime-local"
							bind:value={startDate}
							class="input w-full text-sm"
						/>
					</div>
					<div>
						<label for="end-date" class="block text-xs mb-1" style="color: var(--color-text-muted);">End Date & Time</label>
						<input
							id="end-date"
							type="datetime-local"
							bind:value={endDate}
							class="input w-full text-sm"
						/>
					</div>
				</div>
			{:else}
				<select bind:value={timeRangePreset} class="input w-full text-sm">
					<option value="1m">Last 1 Minute</option>
					<option value="5m">Last 5 Minutes</option>
					<option value="15m">Last 15 Minutes</option>
					<option value="30m">Last 30 Minutes</option>
					<option value="1h">Last Hour</option>
					<option value="6h">Last 6 Hours</option>
					<option value="24h">Last 24 Hours</option>
					<option value="7d">Last 7 Days</option>
					<option value="30d">Last 30 Days</option>
				</select>
			{/if}

			<button
				onclick={handleRefresh}
				disabled={isRefreshing}
				class="btn btn-primary mt-3 w-full flex items-center justify-center gap-2 disabled:opacity-50 disabled:cursor-not-allowed"
			>
				<RefreshCw class="w-4 h-4 {isRefreshing ? 'animate-spin' : ''}" />
				{isRefreshing ? 'Refreshing...' : 'Refresh Data'}
			</button>
		</div>

		<!-- Traffic Type Filter -->
		<div>
			<div class="block text-sm font-medium mb-2" style="color: var(--color-text-base);">Traffic Type</div>
			<div class="space-y-2">
				{#each trafficTypes as { value, label }}
					<label class="flex items-center gap-2 cursor-pointer">
						<input
							type="checkbox"
							checked={selectedTrafficTypes.includes(value)}
							onchange={() => handleTrafficTypeToggle(value)}
							class="rounded"
							style="border-color: var(--color-border-interactive);"
						/>
						<span class="text-sm" style="color: var(--color-text-base);">{label}</span>
					</label>
				{/each}
			</div>
			<div class="flex items-center justify-between mt-2.5">
				<button
					onclick={selectAllTrafficTypes}
					class="text-xs font-medium transition-opacity hover:opacity-70"
					style="color: var(--color-text-success);"
				>
					Select all
				</button>
				{#if selectedTrafficTypes.length > 0}
					<button
						onclick={clearAllTrafficTypes}
						class="text-xs font-medium transition-opacity hover:opacity-70"
						style="color: var(--color-text-primary);"
					>
						Clear ({selectedTrafficTypes.length})
					</button>
				{/if}
			</div>
		</div>

		<!-- Filter Summary -->
		<div class="card p-3 rounded-lg">
			<h4 class="text-sm font-semibold mb-2" style="color: var(--color-text-base);">Active Filters</h4>
			<div class="space-y-1.5 text-xs" style="color: var(--color-text-muted);">
				<div class="flex justify-between">
					<span>Time:</span>
					<span class="font-medium" style="color: var(--color-text-base);">{useCustomTimeRange ? 'Custom' : timeRangePreset}</span>
				</div>
				<div class="flex justify-between">
					<span>Traffic Types:</span>
					<span class="font-medium" style="color: var(--color-text-base);">{selectedTrafficTypes.length}</span>
				</div>
			</div>
		</div>
	</div>

	<!-- Footer with Clear Button -->
	<div class="p-4 border-t" style="border-color: var(--color-border-base);">
		<button onclick={() => filtersStore.reset()} class="btn btn-secondary w-full text-sm">
			Clear All Filters
		</button>
	</div>
</div>
