import { describe, expect, it } from 'vitest';
import { serviceNameResolver } from './service-names';

describe('serviceNameResolver', () => {
	const nameOf = serviceNameResolver(
		{ 'svc:web': { name: 'web-vip', addrs: ['100.100.0.9', 'fd7a:115c:a1e0::9'] }, 'svc:api': { name: '', addrs: ['100.100.0.10'] } },
		{ 'db.example.com': { addrs: ['10.20.0.5'] } }
	);

	it('names service and record addresses', () => {
		expect(nameOf('100.100.0.9')).toBe('web-vip');
		expect(nameOf('fd7a:115c:a1e0:0::9')).toBe('web-vip');
		expect(nameOf('100.100.0.10')).toBe('svc:api');
		expect(nameOf('10.20.0.5')).toBe('db.example.com');
	});

	it('returns nothing for devices and unclaimed addresses', () => {
		expect(nameOf('nLaptop01CNTRL')).toBe('');
		expect(nameOf('8.8.8.8')).toBe('');
		expect(nameOf('')).toBe('');
	});
});
