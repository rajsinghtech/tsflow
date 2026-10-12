import { describe, expect, it } from 'vitest';
import { getPortLabel, getPortName, wellKnownPortName } from './protocol';

describe('port names', () => {
	it('names common homelab services', () => {
		expect(getPortLabel(445, 6)).toBe('SMB (445/TCP)');
		expect(getPortLabel(32400, 6)).toBe('Plex (32400/TCP)');
		expect(getPortLabel(8123, 6)).toBe('HASS (8123/TCP)');
		expect(getPortLabel(8006, 6)).toBe('Proxmox (8006/TCP)');
		expect(getPortLabel(51820, 17)).toBe('WireGuard (51820/UDP)');
	});

	it('keeps existing names and falls back to the number', () => {
		expect(getPortName(22)).toBe('SSH');
		expect(getPortName(41641)).toBe('Tailscale');
		expect(getPortLabel(49288, 6)).toBe('49288/TCP');
	});
});

describe('wellKnownPortName', () => {
	it('returns undefined for an unnamed port so callers pick their own fallback', () => {
		expect(wellKnownPortName(32400)).toBe('Plex');
		expect(wellKnownPortName(49288)).toBeUndefined();
	});
});
