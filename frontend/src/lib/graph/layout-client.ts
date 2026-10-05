import { handleLayoutMessage, layoutItems, type LayoutItem, type LayoutPosition } from './cheap-layout';

export type LayoutEngine = 'worker' | 'main';

interface Pending {
	resolve: (positions: LayoutPosition[]) => void;
}

let worker: Worker | null = null;
let workerBroken = false;
let nextId = 1;
const pending = new Map<number, Pending>();

function getWorker(): Worker | null {
	if (workerBroken || typeof Worker === 'undefined') return null;
	if (worker) return worker;
	try {
		worker = new Worker(new URL('./graph-layout.worker.ts', import.meta.url), { type: 'module' });
		worker.onmessage = (event: MessageEvent<{ requestId: number; positions: LayoutPosition[] }>) => {
			const waiter = pending.get(event.data.requestId);
			if (!waiter) return;
			pending.delete(event.data.requestId);
			waiter.resolve(event.data.positions);
		};
		worker.onerror = () => {
			workerBroken = true;
			worker = null;
		};
		return worker;
	} catch {
		workerBroken = true;
		return null;
	}
}

export async function layoutOffThread(
	items: LayoutItem[],
	spacing: number
): Promise<{ positions: LayoutPosition[]; engine: LayoutEngine }> {
	const current = getWorker();
	if (!current) {
		return { positions: layoutItems(items, spacing), engine: 'main' };
	}

	const requestId = nextId++;
	return new Promise((resolve) => {
		const timer = setTimeout(() => {
			pending.delete(requestId);
			resolve({ positions: layoutItems(items, spacing), engine: 'main' });
		}, 2000);
		pending.set(requestId, {
			resolve: (positions) => {
				clearTimeout(timer);
				resolve({ positions, engine: 'worker' });
			}
		});
		try {
			current.postMessage({ requestId, items, spacing });
		} catch {
			clearTimeout(timer);
			pending.delete(requestId);
			resolve({ positions: handleLayoutMessage({ requestId, items, spacing }).positions, engine: 'main' });
		}
	});
}
