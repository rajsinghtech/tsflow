export const DEFAULT_NEW_PAIR_LOOKBACK = '7d';

export const NEW_PAIR_LOOKBACKS = [
	{ value: '24h', label: '24 hours' },
	{ value: '7d', label: '7 days' },
	{ value: '30d', label: '30 days' }
] as const;

export function newPairPath(
	start: Date,
	end: Date,
	options?: { lookback?: string; limit?: number; offset?: number; trafficTypes?: string[] }
): string {
	const params = new URLSearchParams({
		start: start.toISOString(),
		end: end.toISOString(),
		limit: String(options?.limit ?? 20),
		offset: String(options?.offset ?? 0),
		lookback: options?.lookback || DEFAULT_NEW_PAIR_LOOKBACK
	});
	if (options?.trafficTypes && options.trafficTypes.length > 0) {
		params.set('trafficTypes', options.trafficTypes.join(','));
	}
	return `/analytics/new-pairs?${params.toString()}`;
}

export interface NewPairCoverage {
	lookbackStart?: string;
	dataStart?: string;
	lookbackComplete?: boolean;
}

// lookbackNotice explains a lookback that reaches back before the oldest
// stored data: a pair seen only before then cannot be ruled out, so the list
// may include pairs that are not new. Null when the lookback is covered, or
// when the server did not report coverage.
export function lookbackNotice(coverage: NewPairCoverage | undefined, formatDate: (d: Date) => string): string | null {
	if (!coverage || coverage.lookbackComplete !== false) return null;
	const dataStart = coverage.dataStart ? new Date(coverage.dataStart) : null;
	if (!dataStart || Number.isNaN(dataStart.getTime())) {
		return 'No stored data covers the lookback, so every pair in the window is listed.';
	}
	return `Stored data starts ${formatDate(dataStart)}, inside the lookback. A pair last seen before then shows up here as new.`;
}
