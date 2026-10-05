import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit({
			preprocess: vitePreprocess(),
			adapter: adapter({
				fallback: 'index.html',
				pages: '../backend/frontend/dist',
				assets: '../backend/frontend/dist'
			}),
			// Kit 2 did not poll for a new deployment. Keep that behavior for the
			// embedded static app, which has no server-rendered loads to compare.
			version: {
				pollInterval: 0
			}
		})
	],
	server: {
		port: 3000,
		proxy: {
			'/api': {
				target: 'http://localhost:8080',
				changeOrigin: true
			}
		}
	}
});
