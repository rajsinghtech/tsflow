export interface TimelineBytes {
	txBytes: number;
	rxBytes: number;
}

export interface TimelineBucket {
	time: string;
	seconds?: number;
	virtual: TimelineBytes;
	subnet: TimelineBytes;
	exit: TimelineBytes;
	physical?: TimelineBytes;
}

export interface TimelineColumn {
	time: string;
	virtual: number;
	subnet: number;
	exit: number;
	physical: number;
	total: number;
}

export function bytesOf(part?: TimelineBytes): number {
	if (!part) return 0;
	return (part.txBytes || 0) + (part.rxBytes || 0);
}

// timelineColumns turns API buckets into stacked chart columns.
// Physical is zero unless the bucket includes that series.
export function timelineColumns(buckets: TimelineBucket[]): TimelineColumn[] {
	return buckets.map((bucket) => {
		const virtual = bytesOf(bucket.virtual);
		const subnet = bytesOf(bucket.subnet);
		const exit = bytesOf(bucket.exit);
		const physical = bytesOf(bucket.physical);
		return {
			time: bucket.time,
			virtual,
			subnet,
			exit,
			physical,
			total: virtual + subnet + exit + physical
		};
	});
}

export function selectedDeviceId(explicit: string | null, selectedId: string | null, nodes: { id: string; device?: { id?: string }; ip?: string }[]): string | null {
	if (selectedId) {
		const node = nodes.find((item) => item.id === selectedId);
		if (node?.device?.id) return node.device.id;
		if (node?.ip) return node.ip;
		return selectedId;
	}
	const query = explicit?.trim();
	return query || null;
}
