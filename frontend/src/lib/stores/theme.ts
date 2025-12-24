import { writable } from 'svelte/store';
import { browser } from '$app/environment';

type Theme = 'light' | 'dark' | 'system';

function createThemeStore() {
	const { subscribe, set } = writable<Theme>('system');

	return {
		subscribe,
		set: (theme: Theme) => {
			if (browser) {
				const root = document.documentElement;
				const systemPrefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;

				if (theme === 'system') {
					localStorage.removeItem('theme');
					if (systemPrefersDark) {
						root.classList.add('dark');
					} else {
						root.classList.remove('dark');
					}
				} else if (theme === 'dark') {
					localStorage.setItem('theme', 'dark');
					root.classList.add('dark');
				} else if (theme === 'light') {
					localStorage.setItem('theme', 'light');
					root.classList.remove('dark');
				}
			}
			set(theme);
		},
		init: () => {
			if (browser) {
				const savedTheme = localStorage.getItem('theme') as Theme | null;
				const systemPrefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
				const root = document.documentElement;

				if (savedTheme === 'dark' || savedTheme === 'light') {
					set(savedTheme);
					if (savedTheme === 'dark') {
						root.classList.add('dark');
					} else {
						root.classList.remove('dark');
					}
				} else {
					set('system');
					if (systemPrefersDark) {
						root.classList.add('dark');
					} else {
						root.classList.remove('dark');
					}
				}

				// Listen for system theme changes
				const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
				mediaQuery.addEventListener('change', (e) => {
					const currentTheme = localStorage.getItem('theme');
					if (!currentTheme || currentTheme === 'system') {
						if (e.matches) {
							root.classList.add('dark');
						} else {
							root.classList.remove('dark');
						}
					}
				});
			}
		}
	};
}

export const themeStore = createThemeStore();
