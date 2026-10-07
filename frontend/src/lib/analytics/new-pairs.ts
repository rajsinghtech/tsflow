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
