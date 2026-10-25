import type { TrafficPoint } from './time-window';

// Throughput for the time bar chart. Rates are bits per second, computed from
// the bucketed bandwidth series as bytes * 8 / seconds the bucket covers.

export interface RateBin {
	start: number;
	end: number;
	// Bits per second averaged over the part of the bin that has data.
	// null means no samples, a coverage gap rather than zero traffic.
	bps: number | null;
	// Milliseconds of the bin that have data.
	coveredMs: number;
}

function sampleStep(points: TrafficPoint[]): number {
	let min = Number.POSITIVE_INFINITY;
	for (let index = 1; index < points.length; index++) {
		const gap = points[index].time - points[index - 1].time;
		if (gap > 0 && gap < min) min = gap;
	}
	return Number.isFinite(min) ? min : 60 * 60 * 1000;
}

interface Span {
	start: number;
	end: number;
	bytes: number;
}

// The interval each point's bytes actually cover. A partial first bucket
// covers its tail, a partial later bucket (normally the newest) its head.
function spans(points: TrafficPoint[]): Span[] {
	const ordered = [...points].sort((a, b) => a.time - b.time);
	const fallback = sampleStep(ordered);
	return ordered.map((point, index) => {
		const duration = point.durationMs && point.durationMs > 0 ? point.durationMs : fallback;
		const covered = point.coveredMs && point.coveredMs > 0 ? Math.min(point.coveredMs, duration) : duration;
		const start = index === 0 && ordered.length > 1 && covered < duration ? point.time + duration - covered : point.time;
		return { start, end: start + covered, bytes: point.bytes };
	});
}

function rateOf(span: Span): number {
	const seconds = (span.end - span.start) / 1000;
	return seconds > 0 ? (span.bytes * 8) / seconds : 0;
}

// Spreads each bucket evenly over the time it covers, so a bin narrower than a
// bucket shows the bucket's rate instead of its whole byte count.
export function rateBins(points: TrafficPoint[], domainStart: number, domainEnd: number, binCount: number): RateBin[] {
	const count = Math.max(1, Math.floor(binCount));
	const width = domainEnd - domainStart;
	if (!(width > 0)) return [];
	const bits = new Float64Array(count);
	const covered = new Float64Array(count);
	for (const span of spans(points)) {
		if (span.end <= domainStart || span.start >= domainEnd) continue;
		const rate = rateOf(span);
		const first = Math.max(0, Math.floor(((span.start - domainStart) / width) * count));
		const last = Math.min(count - 1, Math.floor(((span.end - domainStart) / width) * count - 1e-9));
		for (let index = first; index <= last; index++) {
			const binStart = domainStart + (width * index) / count;
			const binEnd = domainStart + (width * (index + 1)) / count;
			const overlap = Math.min(binEnd, span.end) - Math.max(binStart, span.start);
			if (overlap <= 0) continue;
			bits[index] += rate * overlap;
			covered[index] += overlap;
		}
	}
	return Array.from({ length: count }, (_, index) => ({
		start: domainStart + (width * index) / count,
		end: domainStart + (width * (index + 1)) / count,
		bps: covered[index] > 0 ? bits[index] / covered[index] : null,
		coveredMs: covered[index]
	}));
}

// Bytes that fall in each bin, for the bytes view of the same series.
export function byteBins(bins: RateBin[]): { start: number; end: number; bytes: number | null }[] {
	return bins.map((bin) => ({
		start: bin.start,
		end: bin.end,
		bytes: bin.bps === null ? null : (bin.bps * bin.coveredMs) / 8000
	}));
}

// Bars for the Coverage strip. Each bar holds the bytes that fall inside it,
// so minute buckets on 1.5 min bars come out even instead of every other bar
// carrying two whole buckets.
export function coverageBins(points: TrafficPoint[], domainStart: number, domainEnd: number, binCount = 120) {
	return byteBins(rateBins(points, domainStart, domainEnd, binCount));
}

// "16 min", "2 h": how wide one bar is.
export function slotLabel(ms: number): string {
	const minutes = ms / 60_000;
	if (minutes < 90) return `${Math.max(1, Math.round(minutes))} min`;
	const hours = minutes / 60;
	if (hours < 36) return `${parseFloat(hours.toFixed(1))} h`;
	return `${parseFloat((hours / 24).toFixed(1))} d`;
}

// The newest bucket's rate and the highest bucket rate inside [start, end].
export function windowRates(points: TrafficPoint[], start: number, end: number): { latest: number | null; peak: number | null; latestAt: number | null } {
	let latest: number | null = null;
	let latestAt: number | null = null;
	let peak: number | null = null;
	for (const span of spans(points)) {
		if (span.end <= start || span.start >= end) continue;
		const rate = rateOf(span);
		if (latestAt === null || span.start >= latestAt) {
			latest = rate;
			latestAt = span.start;
		}
		if (peak === null || rate > peak) peak = rate;
	}
	return { latest, peak, latestAt };
}

// The highest bin on the chart, so the readout and the marker agree with the line.
export function peakBin(bins: RateBin[]): { index: number; bps: number } | null {
	let best: { index: number; bps: number } | null = null;
	bins.forEach((bin, index) => {
		if (bin.bps !== null && (best === null || bin.bps > best.bps)) best = { index, bps: bin.bps };
	});
	return best;
}

// A round axis top at or above max, close enough that the line fills the chart.
export function niceCeiling(max: number): number {
	if (!(max > 0) || !Number.isFinite(max)) return 1;
	const power = Math.pow(10, Math.floor(Math.log10(max)));
	for (const step of [1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]) {
		if (step * power >= max * (1 - 1e-9)) return step * power;
	}
	return 10 * power;
}

const BIT_UNITS = ['bps', 'Kbps', 'Mbps', 'Gbps', 'Tbps'];

// "940 Mbps", "1.2 Gbps". Picks the unit per value, like the rest of the UI.
export function formatBps(bps: number | null | undefined): string {
	if (bps === null || bps === undefined || !Number.isFinite(bps) || bps <= 0) return '0 bps';
	const index = Math.min(Math.max(0, Math.floor(Math.log10(bps) / 3)), BIT_UNITS.length - 1);
	const value = bps / Math.pow(1000, index);
	const digits = value >= 100 ? 0 : value >= 10 ? 1 : 2;
	return `${parseFloat(value.toFixed(digits))} ${BIT_UNITS[index]}`;
}

// Area and line paths in a 0..100 x 0..height box. Gaps break the line.
export function ratePaths(bins: RateBin[], top: number, height: number, floor: number): { area: string; line: string } {
	const count = bins.length;
	if (!count || !(top > 0)) return { area: '', line: '' };
	const width = 100 / count;
	let area = '';
	let line = '';
	let run: [number, number][] = [];
	const flush = () => {
		if (!run.length) return;
		const head = run.map(([x, y], index) => `${index ? 'L' : 'M'}${x.toFixed(3)},${y.toFixed(3)}`).join('');
		line += head;
		area += `${head}L${run[run.length - 1][0].toFixed(3)},${floor}L${run[0][0].toFixed(3)},${floor}Z`;
		run = [];
	};
	bins.forEach((bin, index) => {
		if (bin.bps === null) {
			flush();
			return;
		}
		const y = floor - (Math.min(bin.bps, top) / top) * height;
		// Both ends of the bin so a lone bin still draws and steps line up with the time axis.
		run.push([index * width, y], [(index + 1) * width, y]);
	});
	flush();
	return { area, line };
}
