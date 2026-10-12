// What to tell someone looking at an empty page when the poller is failing.
// The backend clears lastError after a good poll, so a set error means the
// latest attempt failed.

export interface PollFailure {
	hint: string;
	error: string;
}

interface PollState {
	lastError?: string;
}

const MAX_ERROR_CHARS = 240;

export function pollFailure(status: PollState | null | undefined): PollFailure | null {
	const error = status?.lastError?.trim();
	if (!error) return null;
	return { hint: failureHint(error), error: error.length > MAX_ERROR_CHARS ? `${error.slice(0, MAX_ERROR_CHARS - 1)}…` : error };
}

function failureHint(error: string): string {
	if (/\b(401|403)\b|unauthori[sz]ed|forbidden|token invalid/i.test(error)) {
		return 'The Tailscale API rejected the credentials. Check the API key or OAuth client and that it can read network flow logs.';
	}
	if (/\b404\b|not found/i.test(error)) {
		return 'The Tailscale API could not find this tailnet. Check the tailnet name.';
	}
	if (/\b429\b|rate limit/i.test(error)) {
		return 'The Tailscale API is rate limiting requests. tsflow will keep retrying.';
	}
	return 'The last poll for flow logs failed. tsflow will keep retrying.';
}

// The backend reports a never-set time as year 1, which should read as unknown.
export function realTime(value: string | null | undefined): Date | null {
	if (!value) return null;
	const date = new Date(value);
	return Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1970 ? null : date;
}
