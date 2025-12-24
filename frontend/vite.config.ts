import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [sveltekit(), tailwindcss()],
	server: {
		proxy: {
			'/api': {
				target: process.env.DEVELOPMENT_BACKEND_URL || 'http://localhost:8080',
				changeOrigin: true
			}
		}
	},
	build: {
		chunkSizeWarningLimit: 1000,
		minify: 'esbuild'
	}
});
