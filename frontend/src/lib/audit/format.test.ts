import { describe, expect, it } from 'vitest';
import { actorLabel, targetLabel, diffChunks, diffStats, eventLabel, eventTone, flowLink, isPolicyEvent, lineDiff, relativeTime, userLogin, valueText } from './format';

describe('eventLabel', () => {
	it('names common events and reads the rest', () => {
		expect(eventLabel('NODE.LOGIN')).toBe('Device login');
		expect(eventLabel('TAILNET.UPDATE.ACL')).toBe('Policy file changed');
		expect(eventLabel('DNS.UPDATE.NAMESERVERS')).toBe('Dns update nameservers');
		expect(eventLabel('WEBHOOK.CREATE')).toBe('Webhook create');
	});
});

describe('eventTone', () => {
	it('puts errors first, then the action', () => {
		expect(eventTone({ event: 'NODE.LOGIN', action: 'LOGIN' })).toBe('login');
		expect(eventTone({ event: 'NODE.LOGIN', action: 'LOGIN', error: 'denied' })).toBe('error');
		expect(eventTone({ event: 'USER.DELETE', action: 'DELETE' })).toBe('delete');
		expect(eventTone({ event: 'TAILNET.UPDATE.ACL', action: 'UPDATE' })).toBe('update');
	});
});

describe('labels', () => {
	it('falls back from login to tags to id', () => {
		expect(actorLabel({ actor: { loginName: 'ada@example.com' } })).toBe('ada@example.com');
		expect(actorLabel({ actor: { tags: ['tag:ci'], id: 'k1' } })).toBe('tag:ci');
		expect(actorLabel({ actor: {} })).toBe('unknown');
	});
	it('finds the user for a page link', () => {
		expect(userLogin({ actor: { loginName: 'ada@example.com' }, target: {} })).toBe('ada@example.com');
		expect(userLogin({ actor: { tags: ['tag:ci'] }, target: { type: 'USER', name: 'bo@example.com' } })).toBe('bo@example.com');
		expect(userLogin({ actor: {}, target: { type: 'NODE', name: 'x' } })).toBeNull();
	});
	it('shortens device names in lists', () => {
		const event = { target: { type: 'NODE', name: 'ada-laptop.tail-example.ts.net' } };
		expect(targetLabel(event)).toBe('ada-laptop.tail-example.ts.net');
		expect(targetLabel(event, true)).toBe('ada-laptop');
		expect(targetLabel({ target: { type: 'TAILNET', name: 'tail-example.ts.net' } }, true)).toBe('tail-example.ts.net');
	});
	it('spots policy file edits', () => {
		expect(isPolicyEvent({ event: 'TAILNET.UPDATE.ACL', target: { property: 'ACL' } })).toBe(true);
		expect(isPolicyEvent({ event: 'NODE.UPDATE.ACL_TAGS', target: { property: 'ACL_TAGS' } })).toBe(false);
	});
});

describe('valueText', () => {
	it('keeps strings and pretty-prints the rest', () => {
		expect(valueText('// comment\n{}')).toBe('// comment\n{}');
		expect(valueText(['tag:a'])).toBe('[\n  "tag:a"\n]');
		expect(valueText(null)).toBe('');
	});
});

describe('lineDiff', () => {
	it('marks added and removed lines with line numbers', () => {
		const diff = lineDiff('a\nb\nc', 'a\nB\nc\nd');
		expect(diff.map((l) => `${l.kind}:${l.text}`)).toEqual(['same:a', 'del:b', 'add:B', 'same:c', 'add:d']);
		expect(diff[2].newNo).toBe(2);
		expect(diff[4].newNo).toBe(4);
		expect(diffStats(diff)).toEqual({ added: 2, removed: 1 });
	});
	it('handles empty sides', () => {
		expect(lineDiff('', 'x').map((l) => l.kind)).toEqual(['add']);
		expect(lineDiff('x', '').map((l) => l.kind)).toEqual(['del']);
		expect(lineDiff('', '')).toEqual([]);
	});
	it('stays cheap for one edit in a long file', () => {
		const lines = Array.from({ length: 20000 }, (_, i) => `line ${i}`);
		const after = [...lines];
		after[10000] = 'changed';
		const diff = lineDiff(lines.join('\n'), after.join('\n'));
		expect(diffStats(diff)).toEqual({ added: 1, removed: 1 });
	});
});

describe('diffChunks', () => {
	it('folds unchanged runs outside the context', () => {
		const before = Array.from({ length: 20 }, (_, i) => `l${i}`);
		const after = [...before];
		after[10] = 'x';
		const chunks = diffChunks(lineDiff(before.join('\n'), after.join('\n')), 2);
		expect(chunks.map((c) => c.kind)).toEqual(['skip', 'lines', 'skip']);
		expect(chunks[0]).toEqual({ kind: 'skip', count: 8 });
	});
});

describe('links and times', () => {
	it('opens the traffic page an hour either side', () => {
		const link = flowLink('nAAA', '2026-01-02T03:00:00Z');
		const params = new URLSearchParams(link.slice(2));
		expect(params.get('device')).toBe('nAAA');
		expect(params.get('from')).toBe('2026-01-02T02:00:00.000Z');
		expect(params.get('to')).toBe('2026-01-02T04:00:00.000Z');
	});
	it('reads relative times', () => {
		const now = new Date('2026-01-10T00:00:00Z');
		expect(relativeTime('2026-01-09T23:59:30Z', now)).toBe('30s ago');
		expect(relativeTime('2026-01-09T21:00:00Z', now)).toBe('3h ago');
		expect(relativeTime('2026-01-05T00:00:00Z', now)).toBe('5d ago');
		expect(relativeTime(undefined, now)).toBe('never');
	});
});

describe('catalog labels', () => {
	it('uses the server catalog and falls back for unknown events', async () => {
		const { setCatalog, eventLabel, categoryLabel, eventCategory, originLabel, actorTypeLabel, keyKindLabel, isZeroTime } = await import('./format');
		expect(eventLabel('NODE.APPROVE')).toBe('Device approved');
		setCatalog({
			categories: [{ id: 'keys', label: 'Keys' }],
			events: [{ event: 'API_KEY.EXPIRED', label: 'Key expired after its lifetime', category: 'keys' }]
		});
		expect(eventLabel('API_KEY.EXPIRED')).toBe('Key expired after its lifetime');
		expect(eventCategory('API_KEY.EXPIRED')).toBe('keys');
		expect(categoryLabel('keys')).toBe('Keys');
		expect(categoryLabel('tailnet_lock')).toBe('Tailnet lock');
		expect(eventLabel('WIDGET.UPDATE.COLOR')).toBe('Widget update color');
		expect(originLabel('CONFIG_API')).toBe('API');
		expect(actorTypeLabel('AUTOMATED_WORKER')).toBe('Tailscale service');
		expect(keyKindLabel('oauth_client')).toBe('OAuth client');
		expect(isZeroTime('0001-01-01T00:00:00Z')).toBe(true);
		expect(isZeroTime('2026-01-01T00:00:00Z')).toBe(false);
	});
	it('treats failed requests as errors', async () => {
		const { eventTone } = await import('./format');
		expect(eventTone({ event: 'FAILED_REQUEST.UPDATE', action: 'UPDATE' })).toBe('error');
		expect(eventTone({ event: 'API_KEY.EXPIRED', action: 'EXPIRED' })).toBe('delete');
	});
});
