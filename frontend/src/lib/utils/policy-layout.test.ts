import { describe, expect, it } from 'vitest';
import appCss from '../../app.css?raw';
import layoutSource from './policy-layout.ts?raw';

const LIGHT_NEUTRALS = {
	'--color-policy-ip': '#4b5563',
	'--color-policy-cidr': '#4b5563',
	'--color-policy-wildcard': '#374151',
	'--color-policy-unknown': '#111827'
} as const;

const DARK_NEUTRALS = {
	'--color-policy-ip': '#d1d5db',
	'--color-policy-cidr': '#d1d5db',
	'--color-policy-wildcard': '#e5e7eb',
	'--color-policy-unknown': '#f4f4f5'
} as const;

function extractBlock(css: string, opener: string): string {
	const start = css.indexOf(opener);
	if (start < 0) throw new Error(`missing ${opener}`);
	const brace = css.indexOf('{', start);
	let depth = 0;
	for (let i = brace; i < css.length; i++) {
		if (css[i] === '{') depth++;
		else if (css[i] === '}') {
			depth--;
			if (depth === 0) return css.slice(brace + 1, i);
		}
	}
	throw new Error(`unclosed ${opener}`);
}

function decl(block: string, name: string): string {
	const match = block.match(new RegExp(`${name}\\s*:\\s*(#[0-9a-fA-F]{6})`));
	if (!match) throw new Error(`missing ${name}`);
	return match[1].toLowerCase();
}

function channel(hex: string, index: number): number {
	const raw = hex.startsWith('#') ? hex.slice(1) : hex;
	return Number.parseInt(raw.slice(index, index + 2), 16);
}

function mix(color: string, weight: number, base: string): string {
	const parts = [0, 2, 4].map((index) =>
		Math.round(channel(color, index) * weight + channel(base, index) * (1 - weight))
	);
	return `#${parts.map((part) => part.toString(16).padStart(2, '0')).join('')}`;
}

function luminance(hex: string): number {
	const linear = [0, 2, 4].map((index) => {
		const value = channel(hex, index) / 255;
		return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
	});
	return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
}

function contrast(foreground: string, background: string): number {
	const lighter = Math.max(luminance(foreground), luminance(background));
	const darker = Math.min(luminance(foreground), luminance(background));
	return (lighter + 0.05) / (darker + 0.05);
}

describe('policy neutral node colors', () => {
	const theme = extractBlock(appCss, '@theme');
	const light = extractBlock(appCss, ':root.light');
	const darkCard = decl(theme, '--color-card');
	const lightCard = decl(light, '--color-card');

	it('keeps the original light-mode ink for ip, cidr, wildcard, and unknown nodes', () => {
		for (const [name, hex] of Object.entries(LIGHT_NEUTRALS)) {
			expect(decl(light, name)).toBe(hex);
		}
	});

	it('uses a lighter ink for those nodes in the default dark theme', () => {
		for (const [name, hex] of Object.entries(DARK_NEUTRALS)) {
			expect(decl(theme, name)).toBe(hex);
			expect(hex).not.toBe(LIGHT_NEUTRALS[name as keyof typeof LIGHT_NEUTRALS]);
		}
	});

	it('keeps neutral labels at least AA contrast on node fills and filter chips', () => {
		for (const [palette, card] of [
			[DARK_NEUTRALS, darkCard],
			[LIGHT_NEUTRALS, lightCard]
		] as const) {
			for (const hex of Object.values(palette)) {
				// PolicyNode tints the card with 15% of the ink. Filter chips use 20%.
				expect(contrast(hex, mix(hex, 0.15, card))).toBeGreaterThanOrEqual(4.5);
				expect(contrast(hex, mix(hex, 0.2, card))).toBeGreaterThanOrEqual(4.5);
			}
		}
	});

	it('points neutral node colors at the theme variables and leaves other node colors alone', () => {
		const colors = layoutSource.slice(
			layoutSource.indexOf('export const NODE_COLORS'),
			layoutSource.indexOf('export const EDGE_STYLES')
		);
		expect(colors).toContain("user: '#f59e0b'");
		expect(colors).toContain("group: '#16a34a'");
		expect(colors).toContain("tag: '#db2777'");
		expect(colors).toContain("autogroup: '#7c3aed'");
		expect(colors).toContain("host: '#2563eb'");
		expect(colors).toContain("ipset: '#0891b2'");
		expect(colors).toContain("service: '#dc2626'");
		expect(colors).toContain("ip: 'var(--color-policy-ip)'");
		expect(colors).toContain("cidr: 'var(--color-policy-cidr)'");
		expect(colors).toContain("wildcard: 'var(--color-policy-wildcard)'");
		expect(colors).toContain("unknown: 'var(--color-policy-unknown)'");
		expect(colors).not.toContain('#4b5563');
		expect(colors).not.toContain('#111827');
	});
});
