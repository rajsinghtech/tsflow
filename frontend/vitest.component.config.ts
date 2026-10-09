import { svelte, vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vitest/config';

const clientRuntime = fileURLToPath(new URL('./node_modules/svelte/src/index-client.js', import.meta.url));

export default defineConfig({
	plugins: [svelte({ preprocess: vitePreprocess() })],
	resolve: {
		alias: [
			{ find: '#lib', replacement: fileURLToPath(new URL('./src/lib', import.meta.url)) },
			// Component tests call mount(), which the server runtime does not implement.
			{ find: /^svelte$/, replacement: clientRuntime }
		]
	},
	test: {
		environment: 'jsdom',
		include: ['src/**/*.component.test.ts']
	}
});
