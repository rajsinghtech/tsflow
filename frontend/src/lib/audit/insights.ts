import type { AuditHistogram, AuditKey, AuditNodeSummary } from './types';

// bucketSpanMs is the width of one histogram bar. The backend widens bars
// on long ranges, so it is read from the gap between two bars.
export function bucketSpanMs(histogram: Pick<AuditHistogram, 'bucket' | 'buckets'>): number {
	const base = histogram.bucket === 'hour' ? 3_600_000 : 86_400_000;
	const [a, b] = histogram.buckets;
	if (!a || !b) return base;
	const gap = new Date(b.start).getTime() - new Date(a.start).getTime();
	return gap > 0 ? gap : base;
}

// keyFlags lists what an auth key can do, for a compact table cell.
export function keyFlags(key: Pick<AuditKey, 'reusable' | 'ephemeral' | 'preauthorized'>): string[] {
	const flags: string[] = [];
	if (key.reusable) flags.push('reusable');
	if (key.ephemeral) flags.push('ephemeral');
	if (key.preauthorized) flags.push('pre-approved');
	return flags;
}

// jsonList turns a stored old/new value (usually a JSON array of tags or
// routes) into strings for display.
export function jsonList(value: unknown): string[] {
	if (value === undefined || value === null || value === '') return [];
	if (Array.isArray(value)) return value.map((v) => (typeof v === 'string' ? v : JSON.stringify(v)));
	if (typeof value === 'string') return value ? [value] : [];
	if (typeof value === 'object') {
		const obj = value as Record<string, unknown>;
		for (const key of ['routes', 'tags', 'advertised', 'enabled']) {
			if (Array.isArray(obj[key])) return jsonList(obj[key]);
		}
		return [JSON.stringify(value)];
	}
	return [String(value)];
}

export type ExpiryState = 'disabled' | 'expired' | 'soon' | 'ok' | 'unknown';

// expiryState says whether a device key has expiry turned off, has expired,
// or expires within two weeks.
export function expiryState(summary: Pick<AuditNodeSummary, 'keyExpiry' | 'keyExpiryDisabled' | 'keyExpired'>, now = new Date()): ExpiryState {
	if (summary.keyExpiryDisabled) return 'disabled';
	if (summary.keyExpired) return 'expired';
	if (!summary.keyExpiry) return 'unknown';
	const at = new Date(summary.keyExpiry).getTime();
	if (at <= now.getTime()) return 'expired';
	if (at - now.getTime() < 14 * 86_400_000) return 'soon';
	return 'ok';
}
