import { writable, derived } from 'svelte/store';
import { browser } from '$app/env';

export type ThemeMode = 'light' | 'dark' | 'system';

const STORAGE_KEY = 'tsflow-theme';

function getStoredTheme(): ThemeMode {
	if (!browser) return 'system';
	const stored = localStorage.getItem(STORAGE_KEY);
	if (stored === 'light' || stored === 'dark' || stored === 'system') {
		return stored;
	}
	return 'system';
}

function getSystemPreference(): 'light' | 'dark' {
	if (!browser) return 'dark';
	return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

function createThemeStore() {
	const mode = writable<ThemeMode>(getStoredTheme());

	function applyTheme(resolvedTheme: 'light' | 'dark') {
		if (!browser) return;
		document.documentElement.classList.toggle('light', resolvedTheme === 'light');
	}

	const resolvedTheme = derived(mode, ($mode) => {
		if ($mode === 'system') {
			return getSystemPreference();
		}
		return $mode;
	});

	// Subscribe to changes and apply theme
	if (browser) {
		mode.subscribe(($mode) => {
			localStorage.setItem(STORAGE_KEY, $mode);
			const resolved = $mode === 'system' ? getSystemPreference() : $mode;
			applyTheme(resolved);
		});

		// Listen for system theme changes
		const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
		mediaQuery.addEventListener('change', () => {
			mode.update(($mode) => {
				if ($mode === 'system') {
					applyTheme(getSystemPreference());
				}
				return $mode;
			});
		});

		// Apply initial theme
		const currentMode = getStoredTheme();
		const resolved = currentMode === 'system' ? getSystemPreference() : currentMode;
		applyTheme(resolved);
	}

	return {
		subscribe: mode.subscribe,
		resolvedTheme,
		setMode: (newMode: ThemeMode) => mode.set(newMode),
		toggle: () => {
			mode.update(($mode) => {
				const order: ThemeMode[] = ['light', 'dark', 'system'];
				const currentIndex = order.indexOf($mode);
				return order[(currentIndex + 1) % order.length];
			});
		}
	};
}

export const themeStore = createThemeStore();
