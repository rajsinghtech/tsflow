import { ensureTailnetQuery, withTailnet } from './tailnet-query';

const BASE_URL = '/api';

class APIError extends Error {
	constructor(
		public status: number,
		message: string
	) {
		super(message);
		this.name = 'APIError';
	}
}

async function request<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
	await ensureTailnetQuery();
	const url = `${BASE_URL}${withTailnet(endpoint)}`;

	const response = await fetch(url, {
		...options,
		headers: {
			'Content-Type': 'application/json',
			...options.headers
		}
	});

	if (!response.ok) {
		throw new APIError(response.status, `HTTP ${response.status}: ${response.statusText}`);
	}

	try {
		return await response.json();
	} catch {
		throw new APIError(response.status, 'Invalid JSON response from server');
	}
}

export const api = {
	get: <T>(endpoint: string, options?: { signal?: AbortSignal }) =>
		request<T>(endpoint, { method: 'GET', signal: options?.signal }),
	post: <T>(endpoint: string, body: unknown, options?: { signal?: AbortSignal }) =>
		request<T>(endpoint, {
			method: 'POST',
			body: JSON.stringify(body),
			signal: options?.signal
		})
};

export async function apiUrl(endpoint: string): Promise<string> {
	await ensureTailnetQuery();
	return `${BASE_URL}${withTailnet(endpoint)}`;
}

export { APIError };
