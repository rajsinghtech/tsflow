import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount, tick, unmount } from 'svelte';
import { writable } from 'svelte/store';
import type { TailnetInfo } from '#lib/services/tailnet-query';

const stores = vi.hoisted(() => ({
	tailnets: null as unknown,
	selected: null as unknown
}));

vi.mock('#lib/stores/tailnet-store', () => {
	stores.tailnets = writable<TailnetInfo[]>([]);
	stores.selected = writable<string | null>(null);
	return {
		tailnets: stores.tailnets,
		selectedTailnetId: stores.selected,
		refreshTailnetStatus: vi.fn(async () => {}),
		// Like the real store: the choice lands at once, the URL catches up later.
		selectTailnet: vi.fn(async (id: string) => {
			(stores.selected as import('svelte/store').Writable<string | null>).set(id);
		})
	};
});

import TailnetSwitcher from './TailnetSwitcher.svelte';
import { page } from '$app/state';
import { selectTailnet } from '#lib/stores/tailnet-store';


const running = { running: true, lastError: '' };

describe('TailnetSwitcher', () => {
	let target: HTMLDivElement;
	let component: ReturnType<typeof mount> | null = null;

	afterEach(async () => {
		vi.mocked(selectTailnet).mockClear();
		window.history.replaceState(null, '', '/');
		page.url = new URL('http://localhost/');
		if (component) await unmount(component);
		component = null;
		target?.remove();
	});

	async function open(list: TailnetInfo[]) {
		(stores.tailnets as import('svelte/store').Writable<TailnetInfo[]>).set(list);
		(stores.selected as import('svelte/store').Writable<string | null>).set(list[0].id);
		target = document.createElement('div');
		document.body.appendChild(target);
		component = mount(TailnetSwitcher, { target, props: {} });
		await tick();
		const button = document.querySelector<HTMLButtonElement>('button[aria-label="Tailnet"]');
		button?.click();
		await tick();
		const options = [...document.querySelectorAll('[role="option"]')].map((option) =>
			[...option.querySelectorAll('span')].map((span) => span.textContent?.trim())
		);
		return { button, options };
	}

	it('labels by tailnet name with the id underneath when no display name is set', async () => {
		const { button, options } = await open([
			{ id: 'home', displayName: 'home.example.com', poller: running },
			{ id: 'lab', displayName: 'lab.example.com', poller: running }
		]);
		expect(button?.textContent).toContain('home.example.com');
		expect(button?.title).toBe('Poller running');
		expect(options).toEqual([
			['home.example.com', 'home', 'Poller running'],
			['lab.example.com', 'lab']
		]);
	});

	it('shows the display name with the tailnet name and id underneath', async () => {
		const { button, options } = await open([
			{
				id: 'home',
				displayName: 'Home',
				tailnet: 'home.example.com',
				poller: running
			},
			{ id: 'lab', displayName: 'lab.example.com', poller: running }
		]);
		expect(button?.textContent).toContain('Home');
		expect(button?.textContent).not.toContain('home.example.com');
		expect(button?.title).toBe('home.example.com: Poller running');
		expect(options).toEqual([
			['Home', 'home.example.com · home', 'Poller running'],
			['lab.example.com', 'lab']
		]);
	});

	it('keeps the picked tailnet while the URL still shows the old one', async () => {
		// SvelteKit leaves page.url at the last full navigation after a shallow
		// replaceState, so it still says home after the pick.
		page.url = new URL('http://localhost/?tailnet=home');
		window.history.replaceState(null, '', '/?tailnet=home');
		await open([
			{ id: 'home', displayName: 'Home', poller: running },
			{ id: 'lab', displayName: 'Lab', poller: running },
			{ id: 'cloud', displayName: 'Cloud', poller: running }
		]);
		const lab = [...document.querySelectorAll<HTMLButtonElement>('[role="option"]')].find((option) =>
			option.textContent?.includes('Lab')
		);
		lab?.click();
		await tick();
		await tick();
		expect(vi.mocked(selectTailnet).mock.calls.map(([id]) => id)).toEqual(['lab']);
		expect(document.querySelector('button[aria-label="Tailnet"]')?.textContent).toContain('Lab');
	});

	it('follows the URL on back and forward', async () => {
		window.history.replaceState(null, '', '/?tailnet=home');
		await open([
			{ id: 'home', displayName: 'Home', poller: running },
			{ id: 'lab', displayName: 'Lab', poller: running }
		]);
		window.history.replaceState(null, '', '/?tailnet=lab');
		window.dispatchEvent(new PopStateEvent('popstate'));
		await tick();
		expect(vi.mocked(selectTailnet).mock.calls.map(([id]) => id)).toEqual(['lab']);
		window.history.replaceState(null, '', '/?tailnet=home');
		window.dispatchEvent(new PopStateEvent('popstate'));
		await tick();
		expect(vi.mocked(selectTailnet).mock.calls.map(([id]) => id)).toEqual(['lab', 'home']);
		window.history.replaceState(null, '', '/?tailnet=nope');
		window.dispatchEvent(new PopStateEvent('popstate'));
		await tick();
		expect(vi.mocked(selectTailnet)).toHaveBeenCalledTimes(2);
	});
});
