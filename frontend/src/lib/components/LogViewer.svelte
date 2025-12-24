<script lang="ts">
	import { onMount } from 'svelte';
	import { getProtocolName, formatBytes } from '$lib/utils/format';
	import { extractIP, extractPort, getDeviceName } from '$lib/utils/networkUtils';

	interface LogEntry {
		id: string;
		timestamp: string;
		nodeId: string;
		srcDevice: string;
		dstDevice: string;
		srcIP: string;
		dstIP: string;
		srcPort?: number;
		dstPort?: number;
		protocol: string;
		trafficType: 'virtual' | 'subnet' | 'exit' | 'physical';
		txBytes: number;
		rxBytes: number;
		txPackets: number;
		rxPackets: number;
		timestampMs: number;
	}

	interface Props {
		networkLogs: any[];
		devices: any[];
		searchQuery?: string;
		trafficTypeFilters?: Set<string>;
		selectedNode?: {
			id: string;
			displayName: string;
			ips?: string[];
			ip: string;
		} | null;
		selectedLink?: {
			source: string;
			target: string;
			originalSource: string;
			originalTarget: string;
		} | null;
		onClearSelection?: () => void;
		onHeightChange?: (height: number) => void;
	}

	// Virtual scrolling constants
	const ROW_HEIGHT = 28; // Height of each log row in pixels
	const BUFFER_SIZE = 10; // Number of extra rows to render above/below viewport

	// Panel size constraints
	const PANEL_DEFAULT_HEIGHT = 300;
	const PANEL_MIN_HEIGHT = 150;
	const PANEL_MAX_HEIGHT = 600;
	const PANEL_RESIZE_STEP = 10;

	let {
		networkLogs = [],
		devices = [],
		searchQuery = '',
		trafficTypeFilters = new Set(),
		selectedNode = null,
		selectedLink = null,
		onClearSelection,
		onHeightChange
	}: Props = $props();

	let isExpanded = $state(false);
	let panelHeight = $state(PANEL_DEFAULT_HEIGHT);
	let localSearchQuery = $state('');
	let autoScroll = $state(true);
	let selectedLogId = $state<string | null>(null);
	let isProcessing = $state(false);
	let logEntries = $state<LogEntry[]>([]);
	let scrollContainer = $state<HTMLDivElement | undefined>(undefined);
	let isResizing = $state(false);
	let scrollTop = $state(0);
	let processingCache = $state<{ key: string; entries: LogEntry[] }>({ key: '', entries: [] });

	// Notify parent of height changes
	$effect(() => {
		const height = isExpanded ? panelHeight : 50;
		onHeightChange?.(height);
	});

	// Process logs into entries
	const processLogs = (logs: any[], devices: any[]): LogEntry[] => {
		const entries: LogEntry[] = [];
		let entryId = 0;

		logs.forEach((log) => {
			const timestamp = new Date(log.logged || log.Logged);

			// Warn about invalid timestamps
			if (isNaN(timestamp.getTime())) {
				console.warn('Invalid timestamp in log entry:', log.logged || log.Logged, log);
			}

			const processTraffic = (
				traffic: any[],
				type: 'virtual' | 'subnet' | 'exit' | 'physical'
			) => {
				traffic.forEach((flow) => {
					const srcIP = extractIP(flow.src || flow.Src);
					const dstIP = extractIP(flow.dst || flow.Dst);

					// Validate timestamp - fallback to current time if invalid
					const timestampMs = !isNaN(timestamp.getTime())
						? timestamp.getTime()
						: Date.now();

					entries.push({
						id: `${log.nodeId || log.NodeID}-${entryId++}`,
						timestamp: log.logged || log.Logged,
						timestampMs: timestampMs,
						nodeId: log.nodeId || log.NodeID,
						srcDevice: getDeviceName(srcIP, devices),
						dstDevice: getDeviceName(dstIP, devices),
						srcIP,
						dstIP,
						srcPort: extractPort(flow.src || flow.Src) || undefined,
						dstPort: extractPort(flow.dst || flow.Dst) || undefined,
						protocol: getProtocolName(flow.proto || flow.Proto),
						trafficType: type,
						txBytes: flow.txBytes || flow.TxBytes || 0,
						rxBytes: flow.rxBytes || flow.RxBytes || 0,
						txPackets: flow.txPkts || flow.TxPkts || 0,
						rxPackets: flow.rxPkts || flow.RxPkts || 0
					});
				});
			};

			if (log.virtualTraffic || log.VirtualTraffic) {
				processTraffic(log.virtualTraffic || log.VirtualTraffic, 'virtual');
			}
			if (log.subnetTraffic || log.SubnetTraffic) {
				processTraffic(log.subnetTraffic || log.SubnetTraffic, 'subnet');
			}
			if (log.exitTraffic || log.ExitTraffic) {
				processTraffic(log.exitTraffic || log.ExitTraffic, 'exit');
			}
			if (log.physicalTraffic || log.PhysicalTraffic) {
				processTraffic(log.physicalTraffic || log.PhysicalTraffic, 'physical');
			}
		});

		// Sort by timestamp (newest first)
		entries.sort((a, b) => b.timestampMs - a.timestampMs);
		return entries;
	};

	// Process logs when they change - with caching to avoid reprocessing
	$effect(() => {
		if (networkLogs.length === 0) {
			logEntries = [];
			processingCache = { key: '', entries: [] };
			return;
		}

		// Create a cache key based on logs and devices
		const cacheKey = `${networkLogs.length}-${devices.length}-${JSON.stringify(networkLogs[0]?.logged || '')}`;

		// Use cached entries if available
		if (processingCache.key === cacheKey) {
			logEntries = processingCache.entries;
			return;
		}

		isProcessing = true;
		// Use setTimeout to avoid blocking the UI
		const timeoutId = setTimeout(() => {
			const processed = processLogs(networkLogs, devices);
			logEntries = processed;
			processingCache = { key: cacheKey, entries: processed };
			isProcessing = false;
		}, 0);

		return () => clearTimeout(timeoutId);
	});

	// Filter logs with proper logic
	const filteredLogs = $derived.by(() => {
		if (logEntries.length === 0) return [];

		let filtered = logEntries;
		const searchTerm = (localSearchQuery || searchQuery).toLowerCase().trim();

		// Selected node filter - filter logs involving the selected node
		if (selectedNode) {
			const nodeIPs = selectedNode.ips || [selectedNode.ip];
			filtered = filtered.filter((log) =>
				nodeIPs.includes(log.srcIP) || nodeIPs.includes(log.dstIP)
			);
		}

		// Selected link filter - filter logs matching the specific connection
		if (selectedLink) {
			filtered = filtered.filter((log) =>
				(log.srcIP === selectedLink.source && log.dstIP === selectedLink.target) ||
				(log.srcIP === selectedLink.target && log.dstIP === selectedLink.source) ||
				(selectedLink.originalSource && selectedLink.originalTarget && (
					(log.srcIP === selectedLink.originalSource && log.dstIP === selectedLink.originalTarget) ||
					(log.srcIP === selectedLink.originalTarget && log.dstIP === selectedLink.originalSource)
				))
			);
		}

		// Traffic type filter - apply if filters are set
		if (trafficTypeFilters && trafficTypeFilters.size > 0) {
			filtered = filtered.filter((log) => trafficTypeFilters.has(log.trafficType));
		}

		// Search filter with smart patterns
		if (searchTerm) {
			if (searchTerm.startsWith('ip:')) {
				const ipSearch = searchTerm.substring(3);
				filtered = filtered.filter(
					(log) =>
						log.srcIP.toLowerCase().includes(ipSearch) ||
						log.dstIP.toLowerCase().includes(ipSearch)
				);
			} else if (searchTerm.startsWith('port:')) {
				const portSearch = searchTerm.substring(5);
				const port = parseInt(portSearch, 10);
				if (!isNaN(port)) {
					filtered = filtered.filter((log) =>
						log.srcPort === port || log.dstPort === port
					);
				}
			} else if (searchTerm.startsWith('proto:')) {
				const protoSearch = searchTerm.substring(6).toUpperCase();
				filtered = filtered.filter((log) =>
					log.protocol.toUpperCase().includes(protoSearch)
				);
			} else if (searchTerm.startsWith('type:')) {
				const typeSearch = searchTerm.substring(5).toLowerCase();
				filtered = filtered.filter((log) =>
					log.trafficType.toLowerCase().includes(typeSearch)
				);
			} else {
				// General search across all fields
				filtered = filtered.filter((log) => {
					return (
						log.srcDevice.toLowerCase().includes(searchTerm) ||
						log.dstDevice.toLowerCase().includes(searchTerm) ||
						log.protocol.toLowerCase().includes(searchTerm) ||
						log.srcIP.toLowerCase().includes(searchTerm) ||
						log.dstIP.toLowerCase().includes(searchTerm) ||
						log.trafficType.toLowerCase().includes(searchTerm)
					);
				});
			}
		}

		// Return all filtered logs - virtual scrolling will handle performance
		return filtered;
	});

	// Virtual scrolling - calculate which logs to render
	const visibleLogs = $derived.by(() => {
		if (!isExpanded || filteredLogs.length === 0) return { logs: [], startIndex: 0, offsetTop: 0, totalHeight: 0 };

		// Account for header (40px) + insights bar (24px if shown) + column headers (36px)
		const headerHeight = logStats && !isProcessing ? 100 : 76;
		const containerHeight = panelHeight - headerHeight;
		const visibleRowCount = Math.ceil(containerHeight / ROW_HEIGHT);
		const startIndex = Math.floor(scrollTop / ROW_HEIGHT);
		const endIndex = Math.min(
			filteredLogs.length,
			startIndex + visibleRowCount + BUFFER_SIZE * 2
		);
		const adjustedStartIndex = Math.max(0, startIndex - BUFFER_SIZE);

		return {
			logs: filteredLogs.slice(adjustedStartIndex, endIndex),
			startIndex: adjustedStartIndex,
			offsetTop: adjustedStartIndex * ROW_HEIGHT,
			totalHeight: filteredLogs.length * ROW_HEIGHT
		};
	});

	// Show performance warning if truncated
	const showPerformanceWarning = $derived(filteredLogs.length > 10000);

	// Compute smart statistics for filtered logs
	const logStats = $derived.by(() => {
		if (filteredLogs.length === 0) return null;

		const stats = {
			totalBytes: 0,
			protocols: {} as Record<string, number>,
			trafficTypes: {} as Record<string, number>,
			topSources: {} as Record<string, number>,
			topDestinations: {} as Record<string, number>
		};

		filteredLogs.forEach(log => {
			stats.totalBytes += log.txBytes + log.rxBytes;
			stats.protocols[log.protocol] = (stats.protocols[log.protocol] || 0) + 1;
			stats.trafficTypes[log.trafficType] = (stats.trafficTypes[log.trafficType] || 0) + 1;
			stats.topSources[log.srcDevice] = (stats.topSources[log.srcDevice] || 0) + 1;
			stats.topDestinations[log.dstDevice] = (stats.topDestinations[log.dstDevice] || 0) + 1;
		});

		return {
			totalBytes: stats.totalBytes,
			topProtocol: Object.entries(stats.protocols).sort((a, b) => b[1] - a[1])[0],
			topTrafficType: Object.entries(stats.trafficTypes).sort((a, b) => b[1] - a[1])[0],
			topSource: Object.entries(stats.topSources).sort((a, b) => b[1] - a[1])[0],
			topDestination: Object.entries(stats.topDestinations).sort((a, b) => b[1] - a[1])[0]
		};
	});

	// Format functions
	const formatRelativeTime = (timestamp: string): string => {
		const now = new Date();
		const then = new Date(timestamp);
		const diffMs = now.getTime() - then.getTime();
		const diffSec = Math.floor(diffMs / 1000);
		const diffMin = Math.floor(diffSec / 60);
		const diffHr = Math.floor(diffMin / 60);

		if (diffSec < 60) return `${diffSec}s ago`;
		if (diffMin < 60) return `${diffMin}m ago`;
		if (diffHr < 24) return `${diffHr}h ago`;
		return then.toLocaleDateString();
	};

	const getTrafficTypeColor = (type: string): string => {
		switch (type) {
			case 'virtual':
				return 'var(--color-text-primary)';
			case 'subnet':
				return 'var(--color-text-success)';
			case 'physical':
				return 'var(--color-text-muted)';
			default:
				return 'var(--color-text-muted)';
		}
	};

	// Handle panel resize
	const handleMouseDown = (e: MouseEvent) => {
		isResizing = true;
		const startY = e.clientY;
		const startHeight = panelHeight;

		const handleMouseMove = (e: MouseEvent) => {
			const deltaY = startY - e.clientY;
			const newHeight = Math.max(100, Math.min(window.innerHeight * 0.6, startHeight + deltaY));
			panelHeight = newHeight;
		};

		const handleMouseUp = () => {
			isResizing = false;
			document.removeEventListener('mousemove', handleMouseMove);
			document.removeEventListener('mouseup', handleMouseUp);
			try {
				localStorage.setItem('logViewerHeight', panelHeight.toString());
			} catch (err) {
				console.warn('Failed to save log viewer height:', err);
			}
		};

		document.addEventListener('mousemove', handleMouseMove);
		document.addEventListener('mouseup', handleMouseUp);
	};

	// Load saved height
	onMount(() => {
		try {
			const savedHeight = localStorage.getItem('logViewerHeight');
			if (savedHeight) {
				const parsed = parseInt(savedHeight, 10);
				if (!isNaN(parsed) && parsed > 0) {
					panelHeight = parsed;
				}
			}
		} catch (err) {
			console.warn('Failed to load saved log viewer height:', err);
		}
	});

	// Auto-scroll to bottom
	$effect(() => {
		if (autoScroll && filteredLogs.length > 0 && scrollContainer && isExpanded) {
			const timeoutId = setTimeout(() => {
				if (scrollContainer) {
					scrollContainer.scrollTop = scrollContainer.scrollHeight;
				}
			}, 100);

			return () => clearTimeout(timeoutId);
		}
	});

	// Export logs
	const handleExport = () => {
		const dataStr = JSON.stringify(filteredLogs, null, 2);
		const dataUri = 'data:application/json;charset=utf-8,' + encodeURIComponent(dataStr);
		const exportFileDefaultName = `network-logs-${new Date().toISOString()}.json`;

		const linkElement = document.createElement('a');
		linkElement.setAttribute('href', dataUri);
		linkElement.setAttribute('download', exportFileDefaultName);
		linkElement.click();
	};
</script>

<div
	class="log-viewer-container fixed bottom-0 left-0 right-0 shadow-lg transition-all duration-200 z-20"
	style="background: var(--color-bg-surface); height: {isExpanded ? `${panelHeight}px` : '40px'};"
>
	<!-- Resize handle -->
	{#if isExpanded}
		<div
			role="slider"
			aria-label="Resize log viewer panel"
			aria-orientation="vertical"
			aria-valuenow={panelHeight}
			aria-valuemin={PANEL_MIN_HEIGHT}
			aria-valuemax={PANEL_MAX_HEIGHT}
			tabindex="0"
			class="resize-handle absolute top-0 left-0 right-0 h-1 cursor-ns-resize transition-colors"
			style="background: var(--color-border-interactive);"
			onmousedown={handleMouseDown}
			onkeydown={(e) => {
				if (e.key === 'ArrowUp') {
					e.preventDefault();
					panelHeight = Math.min(panelHeight + PANEL_RESIZE_STEP, PANEL_MAX_HEIGHT);
				} else if (e.key === 'ArrowDown') {
					e.preventDefault();
					panelHeight = Math.max(panelHeight - PANEL_RESIZE_STEP, PANEL_MIN_HEIGHT);
				}
			}}
		></div>
	{/if}

	<!-- Header -->
	<div class="flex items-center justify-between px-4 h-10">
		<div class="flex items-center space-x-4">
			<button
				onclick={() => (isExpanded = !isExpanded)}
				class="toggle-button flex items-center space-x-2 text-sm font-medium transition-colors"
				style="color: var(--color-text-base);"
			>
				<svg
					class="w-4 h-4"
					fill="none"
					stroke="currentColor"
					viewBox="0 0 24 24"
				>
					{#if isExpanded}
						<path
							stroke-linecap="round"
							stroke-linejoin="round"
							stroke-width="2"
							d="M19 9l-7 7-7-7"
						/>
					{:else}
						<path
							stroke-linecap="round"
							stroke-linejoin="round"
							stroke-width="2"
							d="M5 15l7-7 7 7"
						/>
					{/if}
				</svg>
				<span>Network Logs ({filteredLogs.length.toLocaleString()}{filteredLogs.length !== logEntries.length ? ` / ${logEntries.length.toLocaleString()}` : ''} entries)</span>
				{#if isProcessing}
					<svg
						class="w-4 h-4 animate-spin"
						fill="none"
						stroke="currentColor"
						viewBox="0 0 24 24"
					>
						<circle
							class="opacity-25"
							cx="12"
							cy="12"
							r="10"
							stroke="currentColor"
							stroke-width="4"
						/>
						<path
							class="opacity-75"
							fill="currentColor"
							d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
						/>
					</svg>
				{:else if filteredLogs.length > 0}
					<div
						class="flex items-center px-2 py-0.5 text-xs rounded"
						style="background: var(--color-bg-success); color: var(--color-text-success);"
						title="Virtual scrolling active - rendering {visibleLogs.logs?.length || 0} visible rows"
					>
						⚡ Fast
					</div>
				{/if}
				{#if selectedNode || selectedLink}
					<div
						class="flex items-center gap-1 px-2 py-0.5 text-xs rounded-full"
						style="background: var(--color-bg-interactive); color: var(--color-text-primary);"
					>
						<span
							>{selectedNode
								? `Filtered by ${selectedNode.displayName}`
								: 'Filtered by connection'}</span
						>
						{#if onClearSelection}
							<span
								role="button"
								tabindex="0"
								onclick={(e) => {
									e.stopPropagation();
									onClearSelection?.();
								}}
								onkeydown={(e) => {
									if (e.key === 'Enter' || e.key === ' ') {
										e.preventDefault();
										e.stopPropagation();
										onClearSelection?.();
									}
								}}
								class="rounded-full p-0.5 cursor-pointer transition-opacity hover:opacity-70"
								title="Clear filter"
							>
								<svg
									class="w-3 h-3"
									fill="none"
									stroke="currentColor"
									viewBox="0 0 24 24"
								>
									<path
										stroke-linecap="round"
										stroke-linejoin="round"
										stroke-width="2"
										d="M6 18L18 6M6 6l12 12"
									/>
								</svg>
							</span>
						{/if}
					</div>
				{/if}
			</button>

			{#if isExpanded}
				<div class="flex items-center gap-2">
					<div class="relative">
						<svg
							class="absolute left-2 top-1.5 w-4 h-4"
							style="color: var(--color-text-muted);"
							fill="none"
							stroke="currentColor"
							viewBox="0 0 24 24"
						>
							<path
								stroke-linecap="round"
								stroke-linejoin="round"
								stroke-width="2"
								d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"
							/>
						</svg>
						<input
							type="text"
							placeholder="Filter logs (ip:, port:, proto:, type:)"
							bind:value={localSearchQuery}
							class="input pl-8 pr-2 py-1 text-xs rounded-md"
							style="min-width: 250px;"
						/>
					</div>

					<!-- Quick filter buttons -->
					<div class="flex items-center gap-1">
						<button
							onclick={() => localSearchQuery = 'type:virtual'}
							class="px-2 py-1 text-xs rounded transition-colors"
							style="{localSearchQuery === 'type:virtual'
								? 'background: var(--color-bg-interactive); color: var(--color-text-primary);'
								: 'color: var(--color-text-muted);'}"
							title="Show only virtual traffic"
						>
							Virtual
						</button>
						<button
							onclick={() => localSearchQuery = 'proto:TCP'}
							class="px-2 py-1 text-xs rounded transition-colors"
							style="{localSearchQuery === 'proto:TCP'
								? 'background: var(--color-bg-interactive); color: var(--color-text-primary);'
								: 'color: var(--color-text-muted);'}"
							title="Show only TCP traffic"
						>
							TCP
						</button>
						<button
							onclick={() => localSearchQuery = 'proto:UDP'}
							class="px-2 py-1 text-xs rounded transition-colors"
							style="{localSearchQuery === 'proto:UDP'
								? 'background: var(--color-bg-interactive); color: var(--color-text-primary);'
								: 'color: var(--color-text-muted);'}"
							title="Show only UDP traffic"
						>
							UDP
						</button>
						{#if localSearchQuery}
							<button
								onclick={() => localSearchQuery = ''}
								class="px-1.5 py-1 text-xs rounded transition-colors"
								style="color: var(--color-text-muted);"
								title="Clear filter"
							>
								<svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
									<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/>
								</svg>
							</button>
						{/if}
					</div>
				</div>

				<button
					onclick={() => (autoScroll = !autoScroll)}
					class="icon-button p-1 rounded transition-colors {autoScroll ? 'active' : ''}"
					style="{autoScroll
						? 'background: var(--color-bg-interactive); color: var(--color-text-primary);'
						: 'color: var(--color-text-muted);'}"
					title={autoScroll ? 'Auto-scroll enabled' : 'Auto-scroll disabled'}
				>
					<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path
							stroke-linecap="round"
							stroke-linejoin="round"
							stroke-width="2"
							d="M19 14l-7 7m0 0l-7-7m7 7V3"
						/>
					</svg>
				</button>

				<button
					onclick={handleExport}
					class="icon-button p-1 transition-colors"
					style="color: var(--color-text-muted);"
					title="Export logs"
					disabled={isProcessing}
				>
					<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path
							stroke-linecap="round"
							stroke-linejoin="round"
							stroke-width="2"
							d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"
						/>
					</svg>
				</button>
			{/if}
		</div>

		{#if isExpanded && trafficTypeFilters.size > 0 && trafficTypeFilters.size < 4}
			<div class="flex items-center space-x-2 text-xs" style="color: var(--color-text-muted);">
				<span class="px-2 py-1 rounded" style="background: var(--color-bg-interactive);">
					{trafficTypeFilters.size} traffic types
				</span>
			</div>
		{/if}
	</div>

	<!-- Log content -->
	{#if isExpanded}
		<div class="flex flex-col h-full">
			<!-- Smart insights bar -->
			{#if logStats && !isProcessing}
				<div class="flex items-center gap-4 px-4 py-1.5 text-xs border-b" style="background: var(--color-bg-surface); border-color: var(--color-border-base); color: var(--color-text-muted);">
					<span class="font-medium" style="color: var(--color-text-base);">📊 Insights:</span>
					<span title="Total traffic volume">{formatBytes(logStats.totalBytes)}</span>
					{#if logStats.topProtocol}
						<span>Top protocol: <span style="color: var(--color-text-base);">{logStats.topProtocol[0]}</span> ({logStats.topProtocol[1]})</span>
					{/if}
					{#if logStats.topSource}
						<span>Top source: <span style="color: var(--color-text-base);">{logStats.topSource[0]}</span> ({logStats.topSource[1]})</span>
					{/if}
					{#if logStats.topDestination}
						<span>Top dest: <span style="color: var(--color-text-base);">{logStats.topDestination[0]}</span> ({logStats.topDestination[1]})</span>
					{/if}
				</div>
			{/if}

			<!-- Column headers -->
			<div
				class="flex items-center px-4 py-2 text-xs font-medium"
				style="background: var(--color-bg-interactive); color: var(--color-text-base);"
			>
				<div class="w-24">Time</div>
				<div class="flex-1">Source</div>
				<div class="w-8"></div>
				<div class="flex-1">Destination</div>
				<div class="w-16 text-center">Protocol</div>
				<div class="w-16 text-center">Type</div>
				<div class="w-20 text-right">Bytes</div>
			</div>

			<!-- Scrolling container with virtual scrolling -->
			<div
				bind:this={scrollContainer}
				class="log-scroll-container flex-1 overflow-auto"
				style:height="{panelHeight - (logStats && !isProcessing ? 100 : 76)}px"
				onscroll={(e) => {
					const target = e.currentTarget;
					scrollTop = target.scrollTop;
					// Disable auto-scroll when user scrolls up
					if (autoScroll && target.scrollTop < target.scrollHeight - target.clientHeight - 50) {
						autoScroll = false;
					}
				}}
			>
				{#if isProcessing}
					<div class="flex items-center justify-center h-full">
						<div class="text-center">
							<svg
								class="w-8 h-8 animate-spin mx-auto mb-2"
								style="color: var(--color-text-primary);"
								fill="none"
								stroke="currentColor"
								viewBox="0 0 24 24"
							>
								<circle
									class="opacity-25"
									cx="12"
									cy="12"
									r="10"
									stroke="currentColor"
									stroke-width="4"
								/>
								<path
									class="opacity-75"
									fill="currentColor"
									d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
								/>
							</svg>
							<p class="text-sm" style="color: var(--color-text-muted);">
								Processing {networkLogs.length.toLocaleString()} logs
							</p>
						</div>
					</div>
				{:else if filteredLogs.length > 0}
					<!-- Virtual scrolling container with proper height -->
					<div style="height: {visibleLogs.totalHeight}px; position: relative;">
						<!-- Only render visible rows -->
						<div style="transform: translateY({visibleLogs.offsetTop}px);">
							{#each visibleLogs.logs as log (log.id)}
								<div
									role="button"
									tabindex="0"
									class="log-row flex items-center px-4 cursor-pointer text-xs transition-colors {selectedLogId === log.id ? 'selected' : ''}"
									style="height: {ROW_HEIGHT}px;"
									onclick={() => (selectedLogId = log.id)}
									onkeydown={(e) => {
										if (e.key === 'Enter' || e.key === ' ') {
											e.preventDefault();
											selectedLogId = log.id;
										}
									}}
								>
									<div class="w-24" style="color: var(--color-text-muted);" title={new Date(log.timestamp).toLocaleString()}>
										{formatRelativeTime(log.timestamp)}
									</div>
									<div class="flex-1 truncate">
										<span class="font-medium" style="color: var(--color-text-base);">{log.srcDevice}</span>
										{#if log.srcPort}
											<span style="color: var(--color-text-muted);">:{log.srcPort}</span>
										{/if}
									</div>
									<div class="w-8 text-center" style="color: var(--color-text-muted);">→</div>
									<div class="flex-1 truncate">
										<span class="font-medium" style="color: var(--color-text-base);">{log.dstDevice}</span>
										{#if log.dstPort}
											<span style="color: var(--color-text-muted);">:{log.dstPort}</span>
										{/if}
									</div>
									<div class="w-16 text-center" style="color: var(--color-text-base);">{log.protocol}</div>
									<div class="w-16 text-center" style="color: {getTrafficTypeColor(log.trafficType)};">
										{log.trafficType}
									</div>
									<div class="w-20 text-right" style="color: var(--color-text-muted);">
										{formatBytes(log.txBytes + log.rxBytes)}
									</div>
								</div>
							{/each}
						</div>
					</div>
				{:else}
					<div class="flex items-center justify-center h-full" style="color: var(--color-text-muted);">
						No logs to display
					</div>
				{/if}
			</div>
		</div>
	{/if}
</div>

<style>
	/* Hide scrollbar while maintaining scroll functionality */
	.log-scroll-container {
		scrollbar-width: none; /* Firefox */
		-ms-overflow-style: none; /* IE and Edge */
	}

	.log-scroll-container::-webkit-scrollbar {
		display: none; /* Chrome, Safari, Opera */
	}

	.resize-handle:hover {
		background: var(--color-text-primary) !important;
	}

	.toggle-button:hover {
		color: var(--color-text-primary) !important;
	}

	.icon-button:not(.active):hover {
		color: var(--color-text-base) !important;
	}

	.log-row {
		outline: none; /* Remove default focus outline */
	}

	.log-row:hover:not(.selected) {
		background: var(--color-bg-interactive);
	}

	.log-row.selected {
		background: var(--color-bg-interactive);
	}

	.log-row:focus {
		outline: none; /* Remove focus border */
	}

	.log-row:focus-visible {
		outline: 2px solid var(--color-border-interactive); /* Add subtle outline only for keyboard navigation */
		outline-offset: -2px;
	}

	/* Remove weird borders from input and buttons */
	input:focus {
		outline: none;
		box-shadow: 0 0 0 2px var(--color-border-interactive);
	}

	button:focus {
		outline: none;
	}

	button:focus-visible {
		outline: 2px solid var(--color-border-interactive);
		outline-offset: 2px;
	}
</style>
