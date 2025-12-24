import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	kit: {
		adapter: adapter({
			pages: 'dist',
			assets: 'dist',
			fallback: 'index.html',
			precompress: false,
			strict: false
		}),
		prerender: {
			handleMissingId: 'ignore',
			handleHttpError: 'ignore',
			handleUnseenRoutes: 'ignore'
		}
	}
};

export default config;
