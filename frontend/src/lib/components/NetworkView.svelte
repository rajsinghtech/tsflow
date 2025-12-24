<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { browser } from '$app/environment';
	import { devicesStore, networkLogsStore, filtersStore, networkOverviewStore, servicesStore } from '$lib/stores/data';
	import { getProtocolName } from '$lib/utils/format';
	import { formatBytes, extractIP, isTailscaleIP, isPrivateIP } from '$lib/utils/networkUtils';
	import { setupNodeDragging } from '$lib/utils/dragNodes';

	// Dynamic imports for browser-only modules (Sigma requires WebGL)
	let Graph: typeof import('graphology').default;
	let Sigma: typeof import('sigma').default;
	let ELK: typeof import('elkjs/lib/elk.bundled.js').default;

	let container: HTMLDivElement;
	let sigma: InstanceType<typeof Sigma> | null = null;
	let graph = $state<InstanceType<typeof Graph> | null>(null);
	let elk: InstanceType<typeof ELK> | null = null;
	let isInitialized = $state(false);
	let isLayouting = $state(false);
	let selectedNode = $state<string | null>(null);
	let selectedEdge = $state<string | null>(null);
	let hoveredNode = $state<string | null>(null);
	let layoutDebounceTimer: ReturnType<typeof setTimeout> | null = null;
	let nodePositions = $state<Map<string, { x: number; y: number; visible: boolean }>>(new Map());
	let updatePositionsInterval: ReturnType<typeof setInterval> | null = null;
	let cleanupDrag: (() => void) | null = null;
	let isLayoutFrozen = $state(false);
	let lastCameraState: { x: number; y: number; ratio: number } | null = null;
	let preventBrowserZoomHandler: ((e: WheelEvent) => void) | null = null;

	// Memoization caches for performance
	const nodeColorCache = new Map<string, string>();
	const edgeColorCache = new Map<string, string>();

	// Track previous selection state for efficient updates
	let previousSelection: { node: string | null; neighbors: Set<string> } = {
		node: null,
		neighbors: new Set()
	};

	// Zoom/pan controls
	const zoomIn = () => {
		if (!sigma) return;
		const camera = sigma.getCamera();
		camera.animatedZoom({ duration: 200 });
	};

	const zoomOut = () => {
		if (!sigma) return;
		const camera = sigma.getCamera();
		camera.animatedUnzoom({ duration: 200 });
	};

	const fitToView = () => {
		if (!sigma) return;
		const camera = sigma.getCamera();
		camera.animatedReset({ duration: 400 });
	};

	const toggleLayoutFreeze = () => {
		isLayoutFrozen = !isLayoutFrozen;
		// ELK layout is one-shot, no continuous animation to freeze
	};

	// Apply ELK layered layout for clean hierarchical visualization (like production)
	// Uses the same algorithm and settings as the production React Flow implementation
	const applyELKLayout = async (graphInstance: any): Promise<void> => {
		if (!elk) return;

		// Convert graph to ELK format
		const elkNodes: any[] = [];
		const elkEdges: any[] = [];

		graphInstance.forEachNode((nodeId: string) => {
			const attrs = graphInstance.getNodeAttributes(nodeId);
			elkNodes.push({
				id: nodeId,
				width: Math.max(120, attrs.size * 3),  // Node width based on size
				height: 60,  // Fixed height for card-style nodes
			});
		});

		graphInstance.forEachEdge((edgeId: string, attrs: any, source: string, target: string) => {
			elkEdges.push({
				id: edgeId,
				sources: [source],
				targets: [target],
			});
		});

		// ELK layout options - using stress algorithm for network graphs
		// Stress algorithm works better than layered for peer-to-peer network topologies
		const elkGraph = {
			id: 'root',
			layoutOptions: {
				'elk.algorithm': 'stress',
				'elk.stress.desiredEdgeLength': '300',
				'elk.spacing.nodeNode': '150',
				'elk.spacing.componentComponent': '200',
				'elk.aspectRatio': '1.4',
			},
			children: elkNodes,
			edges: elkEdges,
		};

		try {
			const layoutResult = await elk.layout(elkGraph);
			console.log('ELK layout computed:', {
				nodes: layoutResult.children?.length,
				edges: elkEdges.length,
			});

			// Apply ELK positions to graphology nodes
			layoutResult.children?.forEach((node: any) => {
				if (graphInstance.hasNode(node.id)) {
					graphInstance.setNodeAttribute(node.id, 'x', node.x + (node.width || 0) / 2);
					graphInstance.setNodeAttribute(node.id, 'y', node.y + (node.height || 0) / 2);
				}
			});

			console.log('ELK layered layout applied successfully');
		} catch (err) {
			console.error('ELK layout error:', err);
			// Fallback to simple grid layout
			let index = 0;
			const cols = Math.ceil(Math.sqrt(graphInstance.order));
			graphInstance.forEachNode((nodeId: string) => {
				const row = Math.floor(index / cols);
				const col = index % cols;
				graphInstance.setNodeAttribute(nodeId, 'x', col * 200);
				graphInstance.setNodeAttribute(nodeId, 'y', row * 150);
				index++;
			});
		}
	};

	// Helper to check if a point is in the viewport bounds (with margin)
	const isInViewport = (x: number, y: number, margin: number = 250): boolean => {
		if (!sigma) return false;

		const camera = sigma.getCamera();
		const container = sigma.getContainer();
		const width = container.offsetWidth;
		const height = container.offsetHeight;

		// Convert graph coordinates to viewport coordinates
		const viewportCoords = sigma.graphToViewport({ x, y });

		// Check if within viewport bounds plus margin
		return (
			viewportCoords.x >= -margin &&
			viewportCoords.x <= width + margin &&
			viewportCoords.y >= -margin &&
			viewportCoords.y <= height + margin
		);
	};

	// Update node positions for HTML overlays (with viewport culling for performance)
	const updateNodePositions = () => {
		if (!sigma) return;

		const camera = sigma.getCamera();
		const zoom = 1 / camera.ratio; // Inverse ratio for proper scaling
		const newPositions = new Map<string, { x: number; y: number; visible: boolean; scale: number }>();

		// Only render overlays for nodes in or near viewport
		graph.forEachNode((node) => {
			// Get node attributes directly from graph
			const attrs = graph.getNodeAttributes(node);
			if (attrs.x === undefined || attrs.y === undefined) return;

			// Skip nodes outside viewport + margin (performance optimization)
			if (!isInViewport(attrs.x, attrs.y, 300)) return;

			// Convert graph coordinates to screen coordinates
			const screenPos = sigma.graphToViewport({x: attrs.x, y: attrs.y});

			// Mark node as visible if it has valid screen coordinates
			const visible = screenPos.x !== null && screenPos.y !== null && !isNaN(screenPos.x) && !isNaN(screenPos.y);

			newPositions.set(node, {
				x: screenPos.x,
				y: screenPos.y,
				visible,
				scale: Math.max(0.3, Math.min(zoom, 2)) // Clamp scale between 0.3x and 2x
			});
		});

		nodePositions = newPositions;
	};

	// Throttled version that only updates when camera has moved significantly
	const updateNodePositionsThrottled = () => {
		if (!sigma) return;

		const camera = sigma.getCamera();
		const currentState = {
			x: camera.x,
			y: camera.y,
			ratio: camera.ratio
		};

		// Skip if camera hasn't moved significantly (optimization)
		if (lastCameraState &&
			Math.abs(currentState.x - lastCameraState.x) < 1 &&
			Math.abs(currentState.y - lastCameraState.y) < 1 &&
			Math.abs(currentState.ratio - lastCameraState.ratio) < 0.001) {
			return;
		}

		lastCameraState = currentState;
		updateNodePositions();
	};

	// Color constants for different node types
	const NODE_COLORS = {
		derp: '#ef4444',      // Red
		tailscale: '#3b82f6', // Blue
		public: '#f97316',    // Orange
		private: '#8b5cf6',   // Purple
		service: '#a855f7',   // Bright Purple - Services
		selected: '#10b981',  // Green
		highlighted: '#06b6d4', // Cyan
		dimmed: '#9ca3af'     // Gray
	};

	const EDGE_COLORS = {
		virtual: '#3b82f6',   // Blue - Tailscale virtual traffic (like production)
		subnet: '#22c55e',    // Green - Subnet route traffic (like production)
		exit: '#8b5cf6',      // Purple - Exit node traffic
		physical: '#f59e0b',  // Orange/Amber - Physical/DERP traffic (like production)
		selected: '#10b981',  // Green - Selected edge
		dimmed: '#d1d5db'     // Light gray - Dimmed edge
	};

	// Categorize IP addresses based on type
	const categorizeIP = (ip: string, tags: string[]): string => {
		if (tags.some((tag: string) => tag.toLowerCase().includes('derp'))) {
			return 'derp';
		}
		if (isTailscaleIP(ip)) {
			return 'tailscale';
		}
		if (isPrivateIP(ip)) {
			return 'private';
		}
		return 'public';
	};

	// Helper to get device info for an IP
	const getDeviceInfo = (ip: string, devices: any[]) => {
		const device = devices.find((d) => d.addresses?.some((addr: string) => addr === ip));
		if (device) {
			const shortName = device.name?.split('.')[0];
			const displayName = (shortName && shortName.trim()) || device.name?.trim() || ip;
			return {
				displayName,
				tags: device.tags || [],
				online: device.online ?? true,
				isKnownDevice: true
			};
		}
		return {
			displayName: ip,
			tags: [],
			online: false,
			isKnownDevice: false
		};
	};

	// Helper to check if a node matches the search query
	const matchesSearchQuery = (nodeData: any, query: string): boolean => {
		if (!query || query.trim() === '') return true;

		const searchLower = query.toLowerCase().trim();

		if (searchLower.startsWith('tag:')) {
			const tagSearch = searchLower.substring(4);
			return nodeData.tags?.some((tag: string) => tag.toLowerCase().includes(tagSearch)) || false;
		}

		if (searchLower.startsWith('ip:')) {
			const ipSearch = searchLower.substring(3);
			return nodeData.ip.toLowerCase().includes(ipSearch) ||
				   nodeData.ips?.some((ip: string) => ip.toLowerCase().includes(ipSearch));
		}

		if (nodeData.user && searchLower.includes('@')) {
			return nodeData.user.toLowerCase().includes(searchLower);
		}

		return (
			nodeData.displayName.toLowerCase().includes(searchLower) ||
			nodeData.ip.toLowerCase().includes(searchLower) ||
			nodeData.ips?.some((ip: string) => ip.toLowerCase().includes(searchLower)) ||
			nodeData.tags?.some((tag: string) => tag.toLowerCase().includes(searchLower)) ||
			(nodeData.user && nodeData.user.toLowerCase().includes(searchLower))
		);
	};

	// Get node color based on type and state
	const getNodeColor = (nodeData: any, isSelected: boolean, isHighlighted: boolean, isDimmed: boolean): string => {
		if (isSelected) return NODE_COLORS.selected;
		if (isDimmed) return NODE_COLORS.dimmed;
		if (isHighlighted) return NODE_COLORS.highlighted;

		// Check if this is a service node
		if (nodeData.isService || nodeData.isStaticRecord) {
			return NODE_COLORS.service;
		}

		const category = categorizeIP(nodeData.ip, nodeData.tags);
		return NODE_COLORS[category as keyof typeof NODE_COLORS] || NODE_COLORS.public;
	};

	// Cached version for better performance
	const getNodeColorCached = (nodeId: string, nodeData: any, isSelected: boolean, isHighlighted: boolean, isDimmed: boolean): string => {
		const cacheKey = `${nodeId}-${isSelected}-${isHighlighted}-${isDimmed}`;

		if (nodeColorCache.has(cacheKey)) {
			return nodeColorCache.get(cacheKey)!;
		}

		const color = getNodeColor(nodeData, isSelected, isHighlighted, isDimmed);
		nodeColorCache.set(cacheKey, color);
		return color;
	};

	// Get node size based on traffic volume
	const getNodeSize = (totalBytes: number): number => {
		// Logarithmic scale for better visual distribution - larger nodes for visibility
		const minSize = 12;
		const maxSize = 50;
		if (totalBytes === 0) return minSize;
		const logBytes = Math.log10(totalBytes + 1);
		const logMax = Math.log10(1e12); // 1TB
		return minSize + (maxSize - minSize) * Math.min(logBytes / logMax, 1);
	};

	// Get edge color based on type and state
	const getEdgeColor = (edgeData: any, isSelected: boolean, isDimmed: boolean): string => {
		if (isSelected) return EDGE_COLORS.selected;
		if (isDimmed) return EDGE_COLORS.dimmed;
		return EDGE_COLORS[edgeData.trafficType as keyof typeof EDGE_COLORS] || EDGE_COLORS.physical;
	};

	// Cached version for better performance
	const getEdgeColorCached = (edgeId: string, edgeData: any, isSelected: boolean, isDimmed: boolean): string => {
		const cacheKey = `${edgeId}-${isSelected}-${isDimmed}`;

		if (edgeColorCache.has(cacheKey)) {
			return edgeColorCache.get(cacheKey)!;
		}

		const color = getEdgeColor(edgeData, isSelected, isDimmed);
		edgeColorCache.set(cacheKey, color);
		return color;
	};

	// Get edge size based on traffic volume
	const getEdgeSize = (totalBytes: number): number => {
		const minSize = 1;
		const maxSize = 8;
		if (totalBytes === 0) return minSize;
		const logBytes = Math.log10(totalBytes + 1);
		const logMax = Math.log10(1e12); // 1TB
		return minSize + (maxSize - minSize) * Math.min(logBytes / logMax, 1);
	};

	// Build network graph data
	async function buildNetworkData(devices: any[], logs: any[], filters: any, services?: any) {
		const nodeMap = new Map<string, any>();
		const edgeMap = new Map<string, any>();

		if ((!devices || devices.length === 0) && (!logs || logs.length === 0) && (!services?.services || Object.keys(services.services).length === 0)) {
			console.warn('No devices, logs, or services data available');
			return { nodeMap, edgeMap, overviewNodesData: [], overviewLinksData: [] };
		}

		// Build nodes from devices
		(devices || []).forEach((device) => {
			(device.addresses || []).forEach((addr: string) => {
				if (!nodeMap.has(addr)) {
					const shortName = device.name?.split('.')[0];
					const displayName = (shortName && shortName.trim()) || device.name?.trim() || addr;
					nodeMap.set(addr, {
						ip: addr,
						displayName,
						totalBytes: 0,
						txBytes: 0,
						rxBytes: 0,
						connections: 0,
						tags: device.tags || [],
						user: device.user,
						online: device.online,
						isTailscale: isTailscaleIP(addr),
						ips: device.addresses || [addr],
						incomingPorts: new Set<number>(),
						outgoingPorts: new Set<number>(),
						protocols: new Set<string>()
					});
				}
			});
		});

		// Build nodes from VIP services
		if (services?.services) {
			Object.entries(services.services).forEach(([serviceName, serviceInfo]: [string, any]) => {
				(serviceInfo.addrs || []).forEach((addr: string) => {
					if (!nodeMap.has(addr)) {
						nodeMap.set(addr, {
							ip: addr,
							displayName: serviceInfo.name || serviceName,
							totalBytes: 0,
							txBytes: 0,
							rxBytes: 0,
							connections: 0,
							tags: ['service'],
							online: true,
							isTailscale: isTailscaleIP(addr),
							ips: serviceInfo.addrs || [addr],
							incomingPorts: new Set<number>(),
							outgoingPorts: new Set<number>(),
							protocols: new Set<string>(),
							isService: true
						});
					} else {
						// Mark existing node as service
						const node = nodeMap.get(addr);
						node.isService = true;
						node.displayName = serviceInfo.name || serviceName;
					}
				});
			});
		}

		// Build nodes from static DNS records
		if (services?.records) {
			Object.entries(services.records).forEach(([recordName, recordInfo]: [string, any]) => {
				(recordInfo.addrs || []).forEach((addr: string) => {
					if (!nodeMap.has(addr)) {
						nodeMap.set(addr, {
							ip: addr,
							displayName: recordName,
							totalBytes: 0,
							txBytes: 0,
							rxBytes: 0,
							connections: 0,
							tags: ['static-record'],
							online: true,
							isTailscale: isTailscaleIP(addr),
							ips: recordInfo.addrs || [addr],
							incomingPorts: new Set<number>(),
							outgoingPorts: new Set<number>(),
							protocols: new Set<string>(),
							isStaticRecord: true,
							comment: recordInfo.comment
						});
					} else {
						// Mark existing node as static record
						const node = nodeMap.get(addr);
						node.isStaticRecord = true;
						node.displayName = recordName;
						if (recordInfo.comment) {
							node.comment = recordInfo.comment;
						}
					}
				});
			});
		}

		// Build edges from logs
		(logs || []).forEach((log) => {
			const allTraffic = [
				...(log.virtualTraffic || []).map((t: any) => ({ ...t, type: 'virtual' })),
				...(log.subnetTraffic || []).map((t: any) => ({ ...t, type: 'subnet' })),
				...(log.exitTraffic || []).map((t: any) => ({ ...t, type: 'exit' })),
				...(log.physicalTraffic || []).map((t: any) => ({ ...t, type: 'physical' })),
			];

			allTraffic.forEach((traffic) => {
				if (!traffic || typeof traffic !== 'object' || !traffic.src || !traffic.dst) {
					return;
				}

				const proto = traffic.proto !== undefined && traffic.proto !== null
					? traffic.proto
					: traffic.type === 'physical' ? 0 : null;

				if (proto === null) return;

				const protoName = getProtocolName(proto);

				// Apply filters
				if (!filters.protocols.includes(protoName)) return;
				if (!filters.trafficTypes.includes(traffic.type)) return;

				const srcIP = extractIP(traffic.src);
				const dstIP = extractIP(traffic.dst);

				// Apply IP category filter
				const srcNodeForFilter = nodeMap.get(srcIP);
				const dstNodeForFilter = nodeMap.get(dstIP);
				const srcCategory = categorizeIP(srcIP, srcNodeForFilter?.tags || []);
				const dstCategory = categorizeIP(dstIP, dstNodeForFilter?.tags || []);
				if (!filters.ipCategories.includes(srcCategory) && !filters.ipCategories.includes(dstCategory)) {
					return;
				}

				const edgeId = `${srcIP}-${dstIP}-${protoName}-${traffic.type}`;
				const reverseEdgeId = `${dstIP}-${srcIP}-${protoName}-${traffic.type}`;

				// Ensure nodes exist
				if (!nodeMap.has(srcIP)) {
					const deviceInfo = getDeviceInfo(srcIP, devices);
					nodeMap.set(srcIP, {
						ip: srcIP,
						displayName: deviceInfo.displayName,
						totalBytes: 0,
						txBytes: 0,
						rxBytes: 0,
						connections: 0,
						tags: deviceInfo.tags,
						online: deviceInfo.online,
						isTailscale: isTailscaleIP(srcIP),
						ips: [srcIP],
						incomingPorts: new Set<number>(),
						outgoingPorts: new Set<number>(),
						protocols: new Set<string>()
					});
				}
				if (!nodeMap.has(dstIP)) {
					const deviceInfo = getDeviceInfo(dstIP, devices);
					nodeMap.set(dstIP, {
						ip: dstIP,
						displayName: deviceInfo.displayName,
						totalBytes: 0,
						txBytes: 0,
						rxBytes: 0,
						connections: 0,
						tags: deviceInfo.tags,
						online: deviceInfo.online,
						isTailscale: isTailscaleIP(dstIP),
						ips: [dstIP],
						incomingPorts: new Set<number>(),
						outgoingPorts: new Set<number>(),
						protocols: new Set<string>()
					});
				}

				const srcNode = nodeMap.get(srcIP)!;
				const dstNode = nodeMap.get(dstIP)!;

				const txBytes = Math.max(0, typeof traffic.txBytes === 'number' && !isNaN(traffic.txBytes) ? traffic.txBytes : 0);
				const rxBytes = Math.max(0, typeof traffic.rxBytes === 'number' && !isNaN(traffic.rxBytes) ? traffic.rxBytes : 0);
				const txPkts = Math.max(0, typeof traffic.txPkts === 'number' && !isNaN(traffic.txPkts) ? traffic.txPkts : 0);
				const rxPkts = Math.max(0, typeof traffic.rxPkts === 'number' && !isNaN(traffic.rxPkts) ? traffic.rxPkts : 0);
				const bytes = txBytes + rxBytes;

				srcNode.totalBytes += bytes;
				srcNode.txBytes += txBytes;
				srcNode.rxBytes += rxBytes;
				srcNode.connections++;
				srcNode.protocols.add(protoName);

				dstNode.totalBytes += bytes;
				dstNode.rxBytes += txBytes;
				dstNode.txBytes += rxBytes;
				dstNode.connections++;
				dstNode.protocols.add(protoName);

				// Aggregate edges
				if (edgeMap.has(edgeId)) {
					const existing = edgeMap.get(edgeId)!;
					existing.totalBytes += bytes;
					existing.txBytes += txBytes;
					existing.rxBytes += rxBytes;
					existing.packets += txPkts + rxPkts;
				} else if (edgeMap.has(reverseEdgeId)) {
					const reverse = edgeMap.get(reverseEdgeId)!;
					reverse.totalBytes += bytes;
					reverse.txBytes += rxBytes;
					reverse.rxBytes += txBytes;
					reverse.packets += txPkts + rxPkts;
					reverse.bidirectional = true;
				} else {
					edgeMap.set(edgeId, {
						id: edgeId,
						source: srcIP,
						target: dstIP,
						protocol: protoName,
						trafficType: traffic.type,
						totalBytes: bytes,
						txBytes: txBytes,
						rxBytes: rxBytes,
						packets: txPkts + rxPkts,
						bidirectional: false,
					});
				}
			});
		});

		// Filter out nodes with no connections
		const connectedNodeIds = new Set<string>();
		edgeMap.forEach((edge) => {
			connectedNodeIds.add(edge.source);
			connectedNodeIds.add(edge.target);
		});

		// Filter nodes
		const filteredNodes = Array.from(nodeMap.values()).filter((node) => {
			if (!connectedNodeIds.has(node.ip)) return false;
			const category = categorizeIP(node.ip, node.tags);
			if (!filters.ipCategories.includes(category)) return false;
			return matchesSearchQuery(node, filters.searchQuery);
		});

		const filteredNodeIds = new Set(filteredNodes.map((n) => n.ip));

		// Filter edges
		const filteredEdges = Array.from(edgeMap.values()).filter(
			(edge) => filteredNodeIds.has(edge.source) && filteredNodeIds.has(edge.target)
		);

		// Debug: Count traffic types
		const trafficTypeCounts = filteredEdges.reduce((acc, edge) => {
			acc[edge.trafficType] = (acc[edge.trafficType] || 0) + 1;
			return acc;
		}, {} as Record<string, number>);
		console.log('Traffic type counts:', trafficTypeCounts);

		// Prepare overview data
		const overviewNodesData = filteredNodes.map((node) => ({
			id: node.ip,
			displayName: node.displayName,
			totalBytes: node.totalBytes
		}));

		const overviewLinksData = filteredEdges.map((edge) => ({
			id: edge.id,
			protocol: edge.protocol,
			trafficType: edge.trafficType,
			totalBytes: edge.totalBytes
		}));

		return { nodeMap: new Map(filteredNodes.map(n => [n.ip, n])), edgeMap, overviewNodesData, overviewLinksData };
	}

	// Update graph with new data
	async function updateGraph(devices: any[], logs: any[], filters: any, services?: any) {
		console.log('updateGraph called with', devices?.length, 'devices,', logs?.length, 'logs, and', Object.keys(services?.services || {}).length, 'services');

		if (layoutDebounceTimer) {
			clearTimeout(layoutDebounceTimer);
		}

		isLayouting = true;

		layoutDebounceTimer = setTimeout(async () => {
			try {
				console.log('Starting buildNetworkData...');
				const { nodeMap, edgeMap, overviewNodesData, overviewLinksData } = await buildNetworkData(devices, logs, filters, services);
				console.log('buildNetworkData returned:', nodeMap?.size, 'nodes', edgeMap?.size, 'edges');

				// Clear existing graph
				graph.clear();

				// Add nodes to graph with initial positions
				console.log('Adding', nodeMap.size, 'nodes to graph');
				nodeMap.forEach((nodeData, nodeId) => {
					// Destructure to exclude x and y from nodeData spread
					const { x: _x, y: _y, ...dataWithoutPosition } = nodeData as any;
					graph.addNode(nodeId, {
						...dataWithoutPosition,
						label: nodeData.displayName,
						size: getNodeSize(nodeData.totalBytes),
						color: getNodeColor(nodeData, false, false, false),
						x: 0,  // Initial position, will be overwritten by circular layout
						y: 0
					});
				});

				console.log('Graph now has', graph.order, 'nodes');

				// Add edges to graph (must add edges before layout for weight calculations)
				edgeMap.forEach((edgeData) => {
					if (graph.hasNode(edgeData.source) && graph.hasNode(edgeData.target)) {
						try {
							graph.addEdgeWithKey(edgeData.id, edgeData.source, edgeData.target, {
								size: getEdgeSize(edgeData.totalBytes),
								color: getEdgeColor(edgeData, false, false),
								weight: Math.log10(edgeData.totalBytes + 1), // Use log scale for weight
								...edgeData
							});
						} catch (e) {
							// Edge might already exist - skip it
							console.warn(`Failed to add edge ${edgeData.id}:`, e);
						}
					}
				});

				// Apply ELK layered layout for clean hierarchical visualization
				if (graph.order > 0) {
					try {
						await applyELKLayout(graph);
						console.log('ELK layout applied successfully');
					} catch (err) {
						console.error('Error applying ELK layout:', err);
					}

					isLayouting = false;
					sigma?.refresh();
					// Reset camera to fit all nodes
					sigma?.getCamera().animatedReset({ duration: 600 });
					// Update node positions after camera reset
					setTimeout(() => updateNodePositions(), 700);
				} else {
					isLayouting = false;
				}

				// Update overview
				networkOverviewStore.updateOverview(overviewNodesData, overviewLinksData);

				// Refresh sigma
				sigma?.refresh();

				// Update node positions immediately (don't wait for layout)
				updateNodePositions();
			} catch (error) {
				console.error('Error updating graph:', error);
				console.error('Error stack:', error instanceof Error ? error.stack : 'No stack trace');
				console.error('Error message:', error instanceof Error ? error.message : String(error));
				isLayouting = false;
			}
		}, 150);
	}

	// Main effect to process data
	$effect(() => {
		const devices = $devicesStore;
		const logs = $networkLogsStore;
		const filters = $filtersStore;
		const services = $servicesStore;

		// Clear caches when data or filters change
		nodeColorCache.clear();
		edgeColorCache.clear();

		if (isInitialized && sigma && graph) {
			updateGraph(devices, logs, filters, services);
		}
	});

	// Handle selection changes (optimized to only update changed nodes)
	$effect(() => {
		if (!isInitialized || !sigma || !graph) return;

		// Build new selection state
		const currentNeighbors = new Set<string>();
		if (selectedNode) {
			// Find all neighbors of selected node
			graph.forEachNeighbor(selectedNode, (neighbor) => {
				currentNeighbors.add(neighbor);
			});
		}

		// Find nodes that need updates (only those whose state changed)
		const nodesToUpdate = new Set<string>();

		// Always update the newly selected node and previously selected node
		if (selectedNode) nodesToUpdate.add(selectedNode);
		if (previousSelection.node) nodesToUpdate.add(previousSelection.node);

		// Add all current and previous neighbors (their highlight state may have changed)
		currentNeighbors.forEach(n => nodesToUpdate.add(n));
		previousSelection.neighbors.forEach(n => nodesToUpdate.add(n));

		// If we're deselecting everything, need to update all previously dimmed nodes
		if (!selectedNode && previousSelection.node) {
			// All nodes need to be un-dimmed
			graph.forEachNode((node) => {
				if (!previousSelection.neighbors.has(node) && node !== previousSelection.node) {
					nodesToUpdate.add(node);
				}
			});
		}

		// If we're selecting something new, dim all other nodes
		if (selectedNode && !previousSelection.node) {
			// All non-selected/non-neighbor nodes need to be dimmed
			graph.forEachNode((node) => {
				if (node !== selectedNode && !currentNeighbors.has(node)) {
					nodesToUpdate.add(node);
				}
			});
		}

		// Update only affected nodes
		nodesToUpdate.forEach((node) => {
			if (!graph.hasNode(node)) return;

			const nodeData = graph.getNodeAttributes(node);
			const isSelected = selectedNode === node;
			const isHighlighted = currentNeighbors.has(node);
			const isDimmed = selectedNode ? !isSelected && !isHighlighted : false;

			graph.setNodeAttribute(node, 'color', getNodeColorCached(node, nodeData, isSelected, isHighlighted, isDimmed));
			graph.setNodeAttribute(node, 'hidden', false);
		});

		// Update edges (only those connected to affected nodes)
		const edgesToUpdate = new Set<string>();
		nodesToUpdate.forEach((node) => {
			graph.forEachEdge(node, (edge) => {
				edgesToUpdate.add(edge);
			});
		});

		edgesToUpdate.forEach((edge) => {
			const edgeData = graph.getEdgeAttributes(edge);
			const { source, target } = graph.extremities(edge);
			const isSelected = selectedNode ? (source === selectedNode || target === selectedNode) : false;
			const isDimmed = selectedNode && !isSelected;

			graph.setEdgeAttribute(edge, 'color', getEdgeColorCached(edge, edgeData, isSelected, isDimmed));
			graph.setEdgeAttribute(edge, 'hidden', false);
		});

		// Update previous selection state
		previousSelection = { node: selectedNode, neighbors: currentNeighbors };

		sigma?.refresh();
	});

	onMount(async () => {
		if (!browser) return;

		// Dynamically import browser-only modules
		const [graphologyModule, sigmaModule, elkModule] = await Promise.all([
			import('graphology'),
			import('sigma'),
			import('elkjs/lib/elk.bundled.js')
		]);

		Graph = graphologyModule.default;
		Sigma = sigmaModule.default;
		ELK = elkModule.default;

		// Initialize ELK instance
		elk = new ELK();

		// Initialize graph after dynamic imports
		graph = new Graph({ multi: true });

		// Prevent browser zoom on scroll wheel over the graph
		preventBrowserZoomHandler = (e: WheelEvent) => {
			// Only prevent default if Ctrl/Cmd key is pressed (browser zoom gesture)
			// OR if it's a touchpad pinch-zoom gesture (which also triggers wheel with ctrlKey)
			if (e.ctrlKey || e.metaKey) {
				e.preventDefault();
			}
		};
		container.addEventListener('wheel', preventBrowserZoomHandler, { passive: false });

		// Initialize Sigma
		sigma = new Sigma(graph, container, {
			renderEdgeLabels: false,
			renderLabels: false, // Hide node labels since we show HTML overlays
			defaultNodeColor: NODE_COLORS.public,
			defaultEdgeColor: EDGE_COLORS.physical,
			labelFont: 'Inter, system-ui, sans-serif',
			labelSize: 12,
			labelWeight: '500',
			labelColor: { color: '#374151' },
			enableEdgeClickEvents: true,
			enableEdgeHoverEvents: true,
			// Keep node and edge sizes constant in screen pixels regardless of zoom level
			// This prevents overlapping when zooming in/out
			zoomToSizeRatioFunction: () => 1,
		});

		// Node click handler
		sigma.on('clickNode', ({ node }) => {
			selectedNode = selectedNode === node ? null : node;
			selectedEdge = null;
		});

		// Edge click handler
		sigma.on('clickEdge', ({ edge }) => {
			selectedEdge = selectedEdge === edge ? null : edge;
			selectedNode = null;
		});

		// Stage click handler (deselect)
		sigma.on('clickStage', () => {
			selectedNode = null;
			selectedEdge = null;
		});

		// Node hover handler
		sigma.on('enterNode', ({ node }) => {
			hoveredNode = node;
			container.style.cursor = 'pointer';
		});

		sigma.on('leaveNode', () => {
			hoveredNode = null;
			container.style.cursor = 'default';
		});

		// Camera event listeners for position updates
		const camera = sigma.getCamera();
		camera.on('updated', updateNodePositions);

		// Update positions periodically during interaction (throttled for performance)
		updatePositionsInterval = setInterval(updateNodePositionsThrottled, 100);

		// Use requestAnimationFrame for smooth drag updates
		let dragUpdatePending = false;
		const scheduleDragUpdate = () => {
			if (!dragUpdatePending) {
				dragUpdatePending = true;
				requestAnimationFrame(() => {
					updateNodePositions();
					dragUpdatePending = false;
				});
			}
		};

		// Setup node dragging
		cleanupDrag = setupNodeDragging(
			sigma,
			graph,
			(node) => {
				// On drag start, update node positions immediately
				updateNodePositions();
			},
			(node) => {
				// During drag, update positions with requestAnimationFrame
				scheduleDragUpdate();
			},
			(node) => {
				// On drag end, update node positions
				updateNodePositions();
			}
		);

		// Mark as initialized and do initial data load
		isInitialized = true;
		updateGraph($devicesStore, $networkLogsStore, $filtersStore, $servicesStore);
	});

	onDestroy(() => {
		// Remove wheel event listener
		if (preventBrowserZoomHandler && container) {
			container.removeEventListener('wheel', preventBrowserZoomHandler);
			preventBrowserZoomHandler = null;
		}
		if (updatePositionsInterval) {
			clearInterval(updatePositionsInterval);
			updatePositionsInterval = null;
		}
		if (cleanupDrag) {
			cleanupDrag();
			cleanupDrag = null;
		}
		elk = null;
		if (sigma) {
			sigma.kill();
			sigma = null;
		}
		// Clear camera state and caches
		lastCameraState = null;
		nodeColorCache.clear();
		edgeColorCache.clear();
	});
</script>

<div class="w-full h-full relative">
	{#if !isInitialized}
		<div class="absolute inset-0 flex items-center justify-center z-50" style="background: var(--color-bg-app);">
			<div class="flex flex-col items-center gap-3 px-6 py-4 rounded-xl" style="background-color: var(--color-bg-surface); box-shadow: var(--shadow-lg);">
				<div class="animate-spin rounded-full h-8 w-8 border-2 border-transparent" style="border-top-color: var(--color-text-primary); border-right-color: var(--color-text-primary);"></div>
				<p class="text-sm" style="color: var(--color-text-base);">Initializing graph...</p>
			</div>
		</div>
	{:else if isLayouting}
		<div class="absolute inset-0 flex items-center justify-center z-50 pointer-events-none">
			<div class="flex flex-col items-center gap-3 px-6 py-4 rounded-xl backdrop-blur-sm" style="background-color: var(--color-bg-surface); box-shadow: var(--shadow-lg);">
				<div class="animate-spin rounded-full h-8 w-8 border-2 border-transparent" style="border-top-color: var(--color-text-primary); border-right-color: var(--color-text-primary);"></div>
				<p class="text-sm" style="color: var(--color-text-base);">Calculating layout</p>
			</div>
		</div>
	{/if}

	<div bind:this={container} class="w-full h-full" style="background: var(--color-bg-app);"></div>

	<!-- HTML Node Labels - Simple text labels like production -->
	<div class="node-labels-container" style="pointer-events: none;">
		{#each Array.from(nodePositions.entries()) as [nodeId, pos]}
			{#if pos.visible && graph && graph.hasNode(nodeId)}
				{@const nodeData = graph.getNodeAttributes(nodeId)}
				{@const nodeType = nodeData.isService || nodeData.isStaticRecord ? 'service' : nodeData.tags?.some((t: string) => t.toLowerCase().includes('derp')) ? 'derp' : nodeData.isTailscale ? 'tailscale' : 'public'}
				{@const isSelected = selectedNode === nodeId}
				{@const isHighlighted = selectedNode && graph ? (graph.hasEdge(selectedNode, nodeId) || graph.hasEdge(nodeId, selectedNode)) : false}
				{@const isDimmed = selectedNode ? !isSelected && !isHighlighted : false}

				<button
					type="button"
					class="node-label node-label-{nodeType}"
					class:node-label-selected={isSelected}
					class:node-label-highlighted={isHighlighted}
					class:node-label-dimmed={isDimmed}
					style="left: {pos.x}px; top: {pos.y}px; transform: translate(-50%, -50%) scale({pos.scale}); pointer-events: all;"
					onclick={() => {
						selectedNode = selectedNode === nodeId ? null : nodeId;
						selectedEdge = null;
					}}
					aria-label="Select node {nodeData.displayName}"
				>
					<span class="node-label-name">{nodeData.displayName}</span>
					<span class="node-label-traffic">{formatBytes(nodeData.totalBytes)}</span>
				</button>
			{/if}
		{/each}
	</div>

	<!-- Zoom Controls -->
	<div class="absolute bottom-4 right-4 z-10 flex flex-col gap-1 rounded-lg overflow-hidden border" style="background: var(--color-bg-surface); border-color: var(--color-border-base); box-shadow: var(--shadow-md);">
		<button
			onclick={zoomIn}
			class="control-btn"
			title="Zoom in"
			aria-label="Zoom in"
		>
			<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6v12m6-6H6" />
			</svg>
		</button>
		<div style="height: 1px; background: var(--color-border-base);"></div>
		<button
			onclick={zoomOut}
			class="control-btn"
			title="Zoom out"
			aria-label="Zoom out"
		>
			<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M18 12H6" />
			</svg>
		</button>
		<div style="height: 1px; background: var(--color-border-base);"></div>
		<button
			onclick={fitToView}
			class="control-btn"
			title="Fit to view"
			aria-label="Fit to view"
		>
			<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 8V4m0 0h4M4 4l5 5m11-1V4m0 0h-4m4 0l-5 5M4 16v4m0 0h4m-4 0l5-5m11 5l-5-5m5 5v-4m0 4h-4" />
			</svg>
		</button>
		<div style="height: 1px; background: var(--color-border-base);"></div>
		<button
			onclick={toggleLayoutFreeze}
			class="control-btn"
			class:active={isLayoutFrozen}
			title={isLayoutFrozen ? "Unfreeze layout" : "Freeze layout"}
			aria-label={isLayoutFrozen ? "Unfreeze layout" : "Freeze layout"}
		>
			{#if isLayoutFrozen}
				<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
				</svg>
			{:else}
				<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 11V7a4 4 0 118 0m-4 8v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2z" />
				</svg>
			{/if}
		</button>
	</div>

	<!-- Node Selection Panel - DISABLED: All info now shown in overlay cards -->
	<!--
	{#if selectedNode && graph && graph.hasNode(selectedNode)}
		{#key selectedNode}
			{@const nodeData = graph.getNodeAttributes(selectedNode)}
			{@const nodeType = nodeData.tags?.some((t: string) => t.toLowerCase().includes('derp')) ? 'derp' : nodeData.isTailscale ? 'tailscale' : 'public'}
			{@const ipv4Addresses = (nodeData.ips || [nodeData.ip]).filter((ip: string) => !ip.includes(':'))}
			{@const ipv6Addresses = (nodeData.ips || [nodeData.ip]).filter((ip: string) => ip.includes(':'))}
			{@const allPorts = nodeData.incomingPorts && nodeData.outgoingPorts ? Array.from(new Set([...nodeData.incomingPorts, ...nodeData.outgoingPorts])).sort((a, b) => a - b) : []}
			{@const protocols = nodeData.protocols ? Array.from(nodeData.protocols).filter(p => p !== 'Proto-0') : []}
			<div class="node-panel node-panel-{nodeType}">
				<div class="node-panel-header">
					<div class="flex justify-between items-start gap-3">
						<h3 class="node-panel-title">{nodeData.displayName}</h3>
						<div class="flex flex-col items-end shrink-0">
							<span class="node-panel-bytes">{formatBytes(nodeData.totalBytes)}</span>
							<span class="node-panel-conns">{nodeData.connections} conn{nodeData.connections !== 1 ? 's' : ''}</span>
						</div>
					</div>
					{#if nodeData.user}
						<div class="node-panel-user">
							<span class="opacity-70">User:</span> {nodeData.user}
						</div>
					{/if}
				</div>

				<div class="node-panel-body">
					<div class="space-y-1">
						{#each ipv4Addresses as ip}
							<div class="flex items-center gap-2 text-sm">
								<span class="text-xs font-medium w-10" style="color: var(--color-text-muted);">IPv4:</span>
								<code class="font-mono" style="color: var(--color-text-primary);">{ip}</code>
							</div>
						{/each}
						{#each ipv6Addresses.slice(0, 1) as ip}
							<div class="flex items-center gap-2 text-sm">
								<span class="text-xs font-medium w-10" style="color: var(--color-text-muted);">IPv6:</span>
								<code class="font-mono truncate" style="color: var(--color-text-primary);" title={ip}>
									{ip.length > 35 ? `${ip.substring(0, 32)}...` : ip}
								</code>
							</div>
						{/each}
					</div>

					{#if protocols.length > 0}
						<div class="flex items-center gap-2 text-sm">
							<span class="font-medium">{protocols.join(', ')}</span>
						</div>
					{/if}

					{#if allPorts.length > 0}
						<div>
							<div class="flex flex-wrap gap-1.5">
								{#each allPorts.slice(0, 12) as port}
									<span class="port-badge">
										{port}
									</span>
								{/each}
								{#if allPorts.length > 12}
									<span class="port-badge-more">
										+{allPorts.length - 12} more
									</span>
								{/if}
							</div>
						</div>
					{/if}

					{#if nodeData.tags && nodeData.tags.length > 0}
						<div class="flex flex-wrap gap-1.5">
							{#each nodeData.tags.filter((t: string) => t.startsWith('tag:')).slice(0, 6) as tag}
								{@const tagName = tag.replace('tag:', '')}
								{@const tagClass =
									tagName.toLowerCase().includes('k8s') ? 'tag-purple' :
									tagName.toLowerCase().includes('prod') ? 'tag-red' :
									tagName.toLowerCase().includes('dev') ? 'tag-yellow' :
									tagName.toLowerCase().includes('staging') ? 'tag-orange' :
									'tag-gray'
								}
								<span class="tag-badge {tagClass}">
									tag:{tagName}
								</span>
							{/each}
						</div>
					{/if}
				</div>

				<div class="node-panel-footer">
					<div class="flex items-center gap-2">
						{#if nodeData.isTailscale}
							<div class="node-indicator">
								<div class="indicator-dot"></div>
								<span>Tailscale</span>
							</div>
						{/if}
					</div>
					{#if allPorts.length > 0}
						<span class="node-port-count">{allPorts.length} ports</span>
					{/if}
				</div>

				<button
					onclick={() => selectedNode = null}
					class="node-panel-close"
					title="Close"
				>
					<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
					</svg>
				</button>
			</div>
		{/key}
	{/if}
	-->

	<!-- Edge Selection Panel -->
	{#if selectedEdge && graph && graph.hasEdge(selectedEdge)}
		{#key selectedEdge}
			{@const edgeData = graph.getEdgeAttributes(selectedEdge)}
			{@const { source, target } = graph.extremities(selectedEdge)}
			{@const sourceData = graph.getNodeAttributes(source)}
			{@const targetData = graph.getNodeAttributes(target)}
			<div class="selection-panel">
				<div class="flex justify-between items-start mb-3">
					<h3 class="text-lg font-bold" style="color: var(--color-text-base);">Connection</h3>
					<button
						onclick={() => selectedEdge = null}
						class="text-gray-400 hover:text-gray-600 dark:hover:text-gray-200"
						title="Close"
					>
						<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
						</svg>
					</button>
				</div>
				<div class="space-y-3 text-sm">
					<div>
						<span style="color: var(--color-text-muted);" class="block mb-1">From:</span>
						<span class="font-medium" style="color: var(--color-text-primary);">{sourceData.displayName}</span>
						<code class="block text-xs mt-0.5" style="color: var(--color-text-muted);">{source}</code>
					</div>
					<div>
						<span style="color: var(--color-text-muted);" class="block mb-1">To:</span>
						<span class="font-medium" style="color: var(--color-text-primary);">{targetData.displayName}</span>
						<code class="block text-xs mt-0.5" style="color: var(--color-text-muted);">{target}</code>
					</div>
					<div class="flex justify-between">
						<span style="color: var(--color-text-muted);">Protocol:</span>
						<span class="font-medium" style="color: var(--color-text-base);">{edgeData.protocol}</span>
					</div>
					<div class="flex justify-between">
						<span style="color: var(--color-text-muted);">Type:</span>
						<span class="capitalize" style="color: var(--color-text-base);">{edgeData.trafficType}</span>
					</div>
					<div class="flex justify-between">
						<span style="color: var(--color-text-muted);">Total Traffic:</span>
						<span class="font-semibold" style="color: var(--color-text-success);">{formatBytes(edgeData.totalBytes)}</span>
					</div>
					<div class="flex justify-between">
						<span style="color: var(--color-text-muted);">TX:</span>
						<span style="color: var(--color-text-base);">{formatBytes(edgeData.txBytes)}</span>
					</div>
					<div class="flex justify-between">
						<span style="color: var(--color-text-muted);">RX:</span>
						<span style="color: var(--color-text-base);">{formatBytes(edgeData.rxBytes)}</span>
					</div>
					{#if edgeData.packets}
						<div class="flex justify-between">
							<span style="color: var(--color-text-muted);">Packets:</span>
							<span style="color: var(--color-text-base);">{edgeData.packets.toLocaleString()}</span>
						</div>
					{/if}
					{#if edgeData.bidirectional}
						<div class="px-2 py-1 rounded text-xs text-center" style="background: var(--color-bg-interactive); color: var(--color-text-primary);">
							Bidirectional
						</div>
					{/if}
				</div>
			</div>
		{/key}
	{/if}
</div>

<style>
	:global(.dark) {
		--color-bg-app: #111827;
	}

	:global(:not(.dark)) {
		--color-bg-app: #f9fafb;
	}

	.control-btn {
		padding: 0.5rem;
		background: var(--color-bg-surface);
		color: var(--color-text-base);
		border: none;
		cursor: pointer;
		transition: all 150ms ease;
		display: flex;
		align-items: center;
		justify-content: center;
	}

	.control-btn:hover {
		background: var(--color-bg-interactive);
	}

	.control-btn:active {
		background: var(--color-bg-interactive-hover);
	}

	.control-btn.active {
		background: var(--color-bg-interactive);
		color: var(--color-text-primary);
	}

	/* Node Panel Styles */
	.node-panel {
		position: absolute;
		top: 1rem;
		right: 1rem;
		z-index: 10;
		min-width: 280px;
		max-width: 320px;
		border-radius: 0.75rem;
		border-width: 2px;
		background: var(--color-bg-surface);
		border-color: var(--color-border-base);
		box-shadow: var(--shadow-lg);
		transition: all 200ms cubic-bezier(0.4, 0, 0.2, 1);
	}

	.node-panel-derp {
		background: var(--node-bg-derp);
		border-color: rgb(var(--color-red-300));
	}

	.node-panel-tailscale {
		background: var(--node-bg-tailscale);
		border-color: rgb(var(--color-blue-300));
	}

	.node-panel-public {
		background: var(--node-bg-public);
		border-color: rgb(var(--color-orange-300));
	}

	.node-panel-header {
		padding: 0.75rem 1rem;
		border-top-left-radius: 0.625rem;
		border-top-right-radius: 0.625rem;
		border-bottom: 1px solid var(--color-border-base);
		background: var(--node-header-bg);
	}

	.node-panel-title {
		font-size: 1rem;
		font-weight: 700;
		flex: 1;
		color: var(--color-text-base);
		word-break: break-word;
		overflow-wrap: anywhere;
		line-height: 1.3;
	}

	.node-panel-bytes {
		font-size: 0.875rem;
		font-weight: 700;
		color: var(--color-text-success);
	}

	.node-panel-conns {
		font-size: 0.75rem;
		color: var(--color-text-muted);
	}

	.node-panel-user {
		margin-top: 0.5rem;
		font-size: 0.75rem;
		color: var(--color-text-muted);
	}

	.node-panel-body {
		padding: 0.75rem 1rem;
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}

	.node-panel-footer {
		padding: 0.5rem 1rem;
		border-top: 1px solid var(--color-border-base);
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	.node-panel-close {
		position: absolute;
		top: 0.75rem;
		right: 0.75rem;
		padding: 0.25rem;
		background: transparent;
		border: none;
		color: var(--color-text-muted);
		cursor: pointer;
		border-radius: 0.25rem;
		transition: all 150ms ease;
		opacity: 0.6;
	}

	.node-panel-close:hover {
		background: var(--color-bg-interactive);
		color: var(--color-text-base);
		opacity: 1;
	}

	.node-indicator {
		display: flex;
		align-items: center;
		gap: 0.375rem;
		font-size: 0.75rem;
		color: var(--color-text-primary);
	}

	.indicator-dot {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 9999px;
		background-color: var(--color-text-primary);
		animation: pulse 2s cubic-bezier(0.4, 0, 0.6, 1) infinite;
	}

	@keyframes pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.5; }
	}

	.node-port-count {
		font-size: 0.75rem;
		color: var(--color-text-muted);
	}

	.port-badge {
		display: inline-flex;
		align-items: center;
		padding: 0.25rem 0.5rem;
		font-size: 0.75rem;
		font-family: ui-monospace, monospace;
		border-radius: 9999px;
		background: var(--color-bg-interactive);
		color: var(--color-text-primary);
		border: 1px solid var(--color-border-interactive);
		transition: opacity 150ms ease;
	}

	.port-badge:hover {
		opacity: 0.7;
	}

	.port-badge-more {
		display: inline-flex;
		align-items: center;
		padding: 0.25rem 0.5rem;
		font-size: 0.75rem;
		border-radius: 9999px;
		background: var(--color-bg-interactive);
		color: var(--color-text-muted);
	}

	.tag-badge {
		display: inline-flex;
		align-items: center;
		padding: 0.25rem 0.5rem;
		font-size: 0.75rem;
		font-weight: 500;
		border-radius: 9999px;
		transition: transform 150ms ease;
	}

	.tag-badge:hover {
		transform: scale(1.05);
	}

	:global(.tag-purple) {
		background-color: rgb(var(--color-blue-100));
		color: rgb(var(--color-blue-700));
	}

	:global(.dark .tag-purple) {
		background-color: rgb(var(--color-blue-800));
		color: rgb(var(--color-blue-200));
	}

	:global(.tag-red) {
		background-color: rgb(var(--color-red-100));
		color: rgb(var(--color-red-700));
	}

	:global(.dark .tag-red) {
		background-color: rgb(var(--color-red-800));
		color: rgb(var(--color-red-200));
	}

	:global(.tag-yellow) {
		background-color: rgb(var(--color-yellow-100));
		color: rgb(var(--color-yellow-700));
	}

	:global(.dark .tag-yellow) {
		background-color: rgb(var(--color-yellow-800));
		color: rgb(var(--color-yellow-200));
	}

	:global(.tag-orange) {
		background-color: rgb(var(--color-orange-100));
		color: rgb(var(--color-orange-700));
	}

	:global(.dark .tag-orange) {
		background-color: rgb(var(--color-orange-800));
		color: rgb(var(--color-orange-200));
	}

	:global(.tag-gray) {
		background-color: rgb(var(--color-gray-200));
		color: rgb(var(--color-gray-700));
	}

	:global(.dark .tag-gray) {
		background-color: rgb(var(--color-gray-700));
		color: rgb(var(--color-gray-200));
	}

	.selection-panel {
		position: absolute;
		top: 1rem;
		right: 1rem;
		z-index: 10;
		background: var(--color-bg-surface);
		border: 1px solid var(--color-border-base);
		border-radius: 0.75rem;
		box-shadow: var(--shadow-lg);
		padding: 1rem;
		max-width: 24rem;
		min-width: 20rem;
	}

	/* Node Labels Container */
	.node-labels-container {
		position: absolute;
		top: 0;
		left: 0;
		width: 100%;
		height: 100%;
		pointer-events: none;
		z-index: 5;
		contain: layout style paint;
		content-visibility: auto;
	}

	/* Simple Node Label Styles - compact and clean */
	.node-label {
		/* Reset button styles */
		padding: 0.2rem 0.4rem;
		border: none;
		font: inherit;
		color: inherit;
		text-align: center;

		/* Label styles */
		position: absolute;
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 0.1rem;
		border-radius: 0.375rem;
		border: 2px solid;
		box-shadow: 0 2px 8px rgba(0, 0, 0, 0.15);
		transition: all 150ms ease;
		cursor: pointer;
		font-size: 0.6rem;
		transform: translate3d(-50%, -50%, 0);
		will-change: transform, opacity;
		backface-visibility: hidden;
		white-space: nowrap;
		max-width: 110px;
	}

	.node-label:hover {
		box-shadow: var(--shadow-md);
		transform: translate3d(-50%, -50%, 0) scale(1.1);
		z-index: 10;
	}

	.node-label-name {
		font-weight: 600;
		color: var(--color-text-base);
		overflow: hidden;
		text-overflow: ellipsis;
		max-width: 100px;
	}

	.node-label-traffic {
		font-size: 0.55rem;
		color: rgba(255, 255, 255, 0.9);
		font-weight: 500;
	}

	.node-label-service {
		background: rgba(168, 85, 247, 0.9);
		border-color: rgb(192, 132, 252);
		color: white;
	}

	.node-label-service .node-label-name {
		color: white;
	}

	.node-label-derp {
		background: rgba(239, 68, 68, 0.9);
		border-color: rgb(248, 113, 113);
		color: white;
	}

	.node-label-derp .node-label-name {
		color: white;
	}

	.node-label-tailscale {
		background: rgba(59, 130, 246, 0.9);
		border-color: rgb(96, 165, 250);
		color: white;
	}

	.node-label-tailscale .node-label-name {
		color: white;
	}

	.node-label-public {
		background: rgba(249, 115, 22, 0.9);
		border-color: rgb(251, 146, 60);
		color: white;
	}

	.node-label-public .node-label-name {
		color: white;
	}

	.node-label-selected {
		border-width: 2px;
		border-color: rgb(var(--color-green-400));
		box-shadow: var(--shadow-lg);
		z-index: 20;
	}

	.node-label-highlighted {
		border-color: rgb(var(--color-cyan-400));
	}

	.node-label-dimmed {
		opacity: 0.3;
	}

	.node-overlay-header {
		padding: 0.375rem 0.5rem;
		border-top-left-radius: 0.375rem;
		border-top-right-radius: 0.375rem;
		border-bottom: 1px solid var(--color-border-base);
		background: var(--node-header-bg);
	}

	.node-overlay-title {
		font-size: 0.75rem;
		font-weight: 700;
		color: var(--color-text-base);
		word-break: break-word;
		overflow-wrap: anywhere;
		line-height: 1.2;
	}

	.node-overlay-bytes {
		font-size: 0.65rem;
		font-weight: 700;
		color: var(--color-text-success);
	}

	.node-overlay-conns {
		font-size: 0.625rem;
		color: var(--color-text-muted);
	}

	.node-overlay-user {
		margin-top: 0.25rem;
		font-size: 0.625rem;
		color: var(--color-text-muted);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.node-overlay-body {
		padding: 0.375rem 0.5rem;
		display: flex;
		flex-direction: column;
		gap: 0.375rem;
	}

	.node-overlay-footer {
		padding: 0.25rem 0.5rem;
		border-top: 1px solid var(--color-border-base);
		display: flex;
		justify-content: space-between;
		align-items: center;
		font-size: 0.625rem;
	}

	.port-badge-small {
		display: inline-flex;
		align-items: center;
		padding: 0.125rem 0.25rem;
		font-size: 0.625rem;
		font-family: ui-monospace, monospace;
		border-radius: 0.25rem;
		background: var(--color-bg-interactive);
		color: var(--color-text-primary);
		border: 1px solid var(--color-border-interactive);
	}

	.port-badge-more-small {
		display: inline-flex;
		align-items: center;
		padding: 0.125rem 0.25rem;
		font-size: 0.625rem;
		border-radius: 0.25rem;
		background: var(--color-bg-interactive);
		color: var(--color-text-muted);
	}

	.tag-badge-small {
		display: inline-flex;
		align-items: center;
		padding: 0.125rem 0.25rem;
		font-size: 0.625rem;
		font-weight: 500;
		border-radius: 0.25rem;
	}

	.node-indicator-small {
		display: flex;
		align-items: center;
		gap: 0.25rem;
		font-size: 0.625rem;
		color: var(--color-text-primary);
	}

	.indicator-dot-small {
		width: 0.375rem;
		height: 0.375rem;
		border-radius: 9999px;
		background-color: var(--color-text-primary);
		animation: pulse 2s cubic-bezier(0.4, 0, 0.6, 1) infinite;
	}

	.node-port-count-small {
		font-size: 0.625rem;
		color: var(--color-text-muted);
	}
</style>
