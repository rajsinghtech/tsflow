export interface BandwidthPoint {
	time: Date;
	txBytes: number;
	rxBytes: number;
	seconds: number;
}

// Above this many slots the chart is too dense for gaps to show; plot as is.
const MAX_FILLED_SLOTS = 5000;

// The API only returns buckets that have traffic. Joining those points
// directly draws a line straight across quiet stretches, as if traffic kept
// flowing. Fill each missing bucket in the window with a zero point.
export function fillBandwidthGaps(points: BandwidthPoint[], start: Date, end: Date, bucketSeconds: number): BandwidthPoint[] {
	const sorted = [...points].sort((a, b) => a.time.getTime() - b.time.getTime());
	const stepMs = Math.max(1, Math.floor(bucketSeconds)) * 1000;
	const startMs = start.getTime();
	const endMs = end.getTime();
	if (!Number.isFinite(startMs) || !Number.isFinite(endMs) || endMs <= startMs) return sorted;
	const firstSlot = Math.floor(startMs / stepMs) * stepMs;
	if ((endMs - firstSlot) / stepMs > MAX_FILLED_SLOTS) return sorted;

	const byTime = new Map(sorted.map((point) => [point.time.getTime(), point]));
	const filled: BandwidthPoint[] = [];
	for (let slot = firstSlot; slot < endMs; slot += stepMs) {
		const existing = byTime.get(slot);
		if (existing) {
			filled.push(existing);
			byTime.delete(slot);
		} else if (slot >= startMs) {
			filled.push({ time: new Date(slot), txBytes: 0, rxBytes: 0, seconds: stepMs / 1000 });
		}
	}
	// Keep anything that wasn't on the bucket grid rather than dropping it.
	if (byTime.size > 0) {
		filled.push(...byTime.values());
		filled.sort((a, b) => a.time.getTime() - b.time.getTime());
	}
	return filled;
}

// Plot points for the line and area: the last bucket is held until its end
// (capped at the window end), so the newest bucket has width instead of
// ending at its start.
export function bandwidthPlotPoints(points: BandwidthPoint[], end: Date): BandwidthPoint[] {
	if (points.length === 0) return points;
	const last = points[points.length - 1];
	const lastEnd = Math.min(last.time.getTime() + last.seconds * 1000, end.getTime());
	if (!Number.isFinite(lastEnd) || lastEnd <= last.time.getTime()) return points;
	return [...points, { ...last, time: new Date(lastEnd) }];
}
