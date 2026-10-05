import { fileURLToPath, URL } from 'node:url';
import { defineConfig } from 'vitest/config';

export default defineConfig({
	resolve: {
		alias: {
			$lib: fileURLToPath(new URL('./src/lib', import.meta.url))
		}
	},
	test: {
		environment: 'node',
		include: ['src/**/*.test.ts'],
		// Let the policy color test read app.css. Other CSS stays stubbed.
		css: {
			include: [/app\.css/]
		}
	}
});
