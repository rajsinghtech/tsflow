export interface LayoutItem {
	id: string;
	width: number;
	height: number;
}

export interface LayoutPosition {
	id: string;
	x: number;
	y: number;
}

export interface LayoutRequest {
	requestId: number;
	items: LayoutItem[];
	spacing: number;
}

export interface LayoutResponse {
	requestId: number;
	positions: LayoutPosition[];
}

// Grid placement. Column count is sqrt(n), same shape as the old fallback,
// but it only runs for the grouped view and it does not mount those nodes.
export function layoutItems(items: LayoutItem[], spacing: number): LayoutPosition[] {
	const count = items.length;
	if (count === 0) return [];
	const cols = Math.ceil(Math.sqrt(count));
	const positions = new Array<LayoutPosition>(count);
	for (let index = 0; index < count; index++) {
		const item = items[index];
		const row = Math.floor(index / cols);
		const col = index % cols;
		positions[index] = {
			id: item.id,
			x: col * (item.width + spacing),
			y: row * (item.height + spacing)
		};
	}
	return positions;
}

export function handleLayoutMessage(request: LayoutRequest): LayoutResponse {
	return {
		requestId: request.requestId,
		positions: layoutItems(request.items, request.spacing)
	};
}
