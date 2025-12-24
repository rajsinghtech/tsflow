import plugin from 'tailwindcss/plugin';

/** @type {import('tailwindcss').Config} */
export default {
	content: ['./src/**/*.{html,js,svelte,ts}'],
	darkMode: 'selector',
	theme: {
		screens: {
			sm: '420px',
			md: '768px',
			lg: '1024px'
		},
		fontFamily: {
			sans: [
				'Inter',
				'-apple-system',
				'BlinkMacSystemFont',
				'Helvetica',
				'Arial',
				'sans-serif'
			],
			mono: [
				'SFMono-Regular',
				'SFMono Regular',
				'Consolas',
				'Liberation Mono',
				'Menlo',
				'Courier',
				'monospace'
			]
		},
		fontWeight: {
			normal: '400',
			medium: '500',
			semibold: '600',
			bold: '700'
		},
		boxShadow: {
			sm: 'var(--shadow-sm)',
			DEFAULT: 'var(--shadow-base)',
			md: 'var(--shadow-md)',
			lg: 'var(--shadow-lg)',
			xl: 'var(--shadow-xl)',
			'2xl': 'var(--shadow-2xl)',
			inner: 'var(--shadow-inner)',
			none: 'var(--shadow-none)',
			button: 'var(--shadow-button)',
			dialog: 'var(--shadow-dialog)',
			form: 'var(--shadow-form)',
			soft: 'var(--shadow-soft)',
			popover: 'var(--shadow-popover)',
			tooltip: 'var(--shadow-tooltip)'
		},
		colors: {
			transparent: 'transparent',
			current: 'currentColor',
			white: 'rgb(var(--color-white) / <alpha-value>)',
			gray: {
				0: 'rgb(var(--color-gray-0) / <alpha-value>)',
				50: 'rgb(var(--color-gray-50) / <alpha-value>)',
				100: 'rgb(var(--color-gray-100) / <alpha-value>)',
				200: 'rgb(var(--color-gray-200) / <alpha-value>)',
				300: 'rgb(var(--color-gray-300) / <alpha-value>)',
				400: 'rgb(var(--color-gray-400) / <alpha-value>)',
				500: 'rgb(var(--color-gray-500) / <alpha-value>)',
				600: 'rgb(var(--color-gray-600) / <alpha-value>)',
				700: 'rgb(var(--color-gray-700) / <alpha-value>)',
				800: 'rgb(var(--color-gray-800) / <alpha-value>)',
				900: 'rgb(var(--color-gray-900) / <alpha-value>)',
				1000: 'rgb(var(--color-gray-1000) / <alpha-value>)',
				1100: 'rgb(var(--color-gray-1100) / <alpha-value>)'
			},
			blue: {
				0: 'rgb(var(--color-blue-0) / <alpha-value>)',
				50: 'rgb(var(--color-blue-50) / <alpha-value>)',
				100: 'rgb(var(--color-blue-100) / <alpha-value>)',
				200: 'rgb(var(--color-blue-200) / <alpha-value>)',
				300: 'rgb(var(--color-blue-300) / <alpha-value>)',
				400: 'rgb(var(--color-blue-400) / <alpha-value>)',
				500: 'rgb(var(--color-blue-500) / <alpha-value>)',
				600: 'rgb(var(--color-blue-600) / <alpha-value>)',
				700: 'rgb(var(--color-blue-700) / <alpha-value>)',
				800: 'rgb(var(--color-blue-800) / <alpha-value>)',
				900: 'rgb(var(--color-blue-900) / <alpha-value>)'
			},
			green: {
				0: 'rgb(var(--color-green-0) / <alpha-value>)',
				50: 'rgb(var(--color-green-50) / <alpha-value>)',
				100: 'rgb(var(--color-green-100) / <alpha-value>)',
				200: 'rgb(var(--color-green-200) / <alpha-value>)',
				300: 'rgb(var(--color-green-300) / <alpha-value>)',
				400: 'rgb(var(--color-green-400) / <alpha-value>)',
				500: 'rgb(var(--color-green-500) / <alpha-value>)',
				600: 'rgb(var(--color-green-600) / <alpha-value>)',
				700: 'rgb(var(--color-green-700) / <alpha-value>)',
				800: 'rgb(var(--color-green-800) / <alpha-value>)',
				900: 'rgb(var(--color-green-900) / <alpha-value>)'
			},
			red: {
				0: 'rgb(var(--color-red-0) / <alpha-value>)',
				50: 'rgb(var(--color-red-50) / <alpha-value>)',
				100: 'rgb(var(--color-red-100) / <alpha-value>)',
				200: 'rgb(var(--color-red-200) / <alpha-value>)',
				300: 'rgb(var(--color-red-300) / <alpha-value>)',
				400: 'rgb(var(--color-red-400) / <alpha-value>)',
				500: 'rgb(var(--color-red-500) / <alpha-value>)',
				600: 'rgb(var(--color-red-600) / <alpha-value>)',
				700: 'rgb(var(--color-red-700) / <alpha-value>)',
				800: 'rgb(var(--color-red-800) / <alpha-value>)',
				900: 'rgb(var(--color-red-900) / <alpha-value>)'
			},
			yellow: {
				0: 'rgb(var(--color-yellow-0) / <alpha-value>)',
				50: 'rgb(var(--color-yellow-50) / <alpha-value>)',
				100: 'rgb(var(--color-yellow-100) / <alpha-value>)',
				200: 'rgb(var(--color-yellow-200) / <alpha-value>)',
				300: 'rgb(var(--color-yellow-300) / <alpha-value>)',
				400: 'rgb(var(--color-yellow-400) / <alpha-value>)',
				500: 'rgb(var(--color-yellow-500) / <alpha-value>)',
				600: 'rgb(var(--color-yellow-600) / <alpha-value>)',
				700: 'rgb(var(--color-yellow-700) / <alpha-value>)',
				800: 'rgb(var(--color-yellow-800) / <alpha-value>)',
				900: 'rgb(var(--color-yellow-900) / <alpha-value>)'
			},
			orange: {
				0: 'rgb(var(--color-orange-0) / <alpha-value>)',
				50: 'rgb(var(--color-orange-50) / <alpha-value>)',
				100: 'rgb(var(--color-orange-100) / <alpha-value>)',
				200: 'rgb(var(--color-orange-200) / <alpha-value>)',
				300: 'rgb(var(--color-orange-300) / <alpha-value>)',
				400: 'rgb(var(--color-orange-400) / <alpha-value>)',
				500: 'rgb(var(--color-orange-500) / <alpha-value>)',
				600: 'rgb(var(--color-orange-600) / <alpha-value>)',
				700: 'rgb(var(--color-orange-700) / <alpha-value>)',
				800: 'rgb(var(--color-orange-800) / <alpha-value>)',
				900: 'rgb(var(--color-orange-900) / <alpha-value>)'
			}
		},
		extend: {
			colors: {
				'bg-base': 'var(--color-bg-base)',
				'bg-app': 'var(--color-bg-app)',
				'bg-surface': 'var(--color-bg-surface)',
				'bg-interactive': 'var(--color-bg-interactive)',
				'bg-interactive-hover': 'var(--color-bg-interactive-hover)',
				'bg-accent': 'var(--color-bg-accent)',
				'bg-accent-hover': 'var(--color-bg-accent-hover)',
				'bg-menu-item-hover': 'var(--color-bg-menu-item-hover)',

				'border-base': 'var(--color-border-base)',
				'border-primary': 'var(--color-border-primary)',
				'border-focus': 'var(--color-border-focus)',
				'border-focus-warning': 'var(--color-border-focus-warning)',
				'border-focus-danger': 'var(--color-border-focus-danger)',
				'border-interactive': 'var(--color-border-interactive)',
				'border-interactive-hover': 'var(--color-border-interactive-hover)',

				'outline-focus': 'var(--color-outline-focus)',
				'outline-focus-warning': 'var(--color-outline-focus-warning)',
				'outline-focus-danger': 'var(--color-outline-focus-danger)',

				'text-base': 'var(--color-text-base)',
				'text-primary': 'var(--color-text-primary)',
				'text-secondary': 'var(--color-text-secondary)',
				'text-muted': 'var(--color-text-muted)',
				'text-disabled': 'var(--color-text-disabled)',
				'text-success': 'var(--color-text-success)',
				'text-warning': 'var(--color-text-warning)',
				'text-danger': 'var(--color-text-danger)',
				'text-accent': 'var(--color-text-accent)',
				'text-on-interactive': 'var(--color-text-on-interactive)',

				'icon-primary': 'var(--icon-primary)',
				'icon-muted': 'var(--icon-muted)',
				'icon-secondary': 'var(--icon-secondary)'
			},
			borderColor: {
				DEFAULT: 'var(--color-border-base)'
			},
			animation: {
				'scale-in': 'scale-in 120ms cubic-bezier(0.16, 1, 0.3, 1)',
				'scale-out': 'scale-out 120ms cubic-bezier(0.16, 1, 0.3, 1)'
			},
			transformOrigin: {
				'radix-hovercard': 'var(--radix-hover-card-content-transform-origin)',
				'radix-popover': 'var(--radix-popover-content-transform-origin)',
				'radix-tooltip': 'var(--radix-tooltip-content-transform-origin)'
			},
			keyframes: {
				'scale-in': {
					'0%': {
						transform: 'scale(0.94)',
						opacity: '0'
					},
					'100%': {
						transform: 'scale(1)',
						opacity: '1'
					}
				},
				'scale-out': {
					'0%': {
						transform: 'scale(1)',
						opacity: '1'
					},
					'100%': {
						transform: 'scale(0.94)',
						opacity: '0'
					}
				}
			}
		}
	},
	plugins: [
		plugin(function ({ addVariant }) {
			addVariant('state-open', ['&[data-state="open"]', '[data-state="open"] &']);
			addVariant('state-closed', ['&[data-state="closed"]', '[data-state="closed"] &']);
			addVariant('state-delayed-open', [
				'&[data-state="delayed-open"]',
				'[data-state="delayed-open"] &'
			]);
			addVariant('state-active', ['&[data-state="active"]']);
			addVariant('state-inactive', ['&[data-state="inactive"]']);
		})
	]
};
