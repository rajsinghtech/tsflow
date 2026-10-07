import { describe, expect, it } from 'vitest';
import { categorizeIP } from './ip-utils';

describe('categorizeIP', () => {
	it('treats only 100.64.0.0/10 as Tailscale IPv4', () => {
		expect(categorizeIP('100.64.0.1')).toEqual(['tailscale']);
		expect(categorizeIP('100.127.255.254')).toEqual(['tailscale']);
		expect(categorizeIP('100.63.255.255')).toEqual(['public']);
		expect(categorizeIP('100.128.0.1')).toEqual(['public']);
		expect(categorizeIP('100.20.1.2')).toEqual(['public']);
	});

	it('recognises the Tailscale IPv6 prefix in any case', () => {
		expect(categorizeIP('fd7a:115c:a1e0::1')).toEqual(['tailscale']);
		expect(categorizeIP('FD7A:115C:A1E0::1')).toEqual(['tailscale']);
	});

	it('covers RFC 1918 and nothing near it', () => {
		expect(categorizeIP('10.0.0.1')).toEqual(['private']);
		expect(categorizeIP('172.16.0.1')).toEqual(['private']);
		expect(categorizeIP('172.31.255.1')).toEqual(['private']);
		expect(categorizeIP('192.168.1.1')).toEqual(['private']);
		expect(categorizeIP('172.32.0.1')).toEqual(['public']);
		expect(categorizeIP('192.169.0.1')).toEqual(['public']);
	});

	it('covers all of fc00::/7 and fe80::/10', () => {
		expect(categorizeIP('fd12:3456:789a::1')).toEqual(['private']);
		expect(categorizeIP('fc01::1')).toEqual(['private']);
		expect(categorizeIP('FD00::1')).toEqual(['private']);
		expect(categorizeIP('fe80::1')).toEqual(['private']);
		expect(categorizeIP('febf::1')).toEqual(['private']);
		expect(categorizeIP('fec0::1')).toEqual(['public']);
		expect(categorizeIP('fd::1')).toEqual(['public']);
		expect(categorizeIP('2001:db8::1')).toEqual(['public']);
	});

	it('keeps DERP and empty input', () => {
		expect(categorizeIP('127.3.3.40')).toEqual(['derp']);
		expect(categorizeIP('')).toEqual(['null']);
	});
});
