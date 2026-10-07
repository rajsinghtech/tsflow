import { describe, expect, it } from 'vitest';
import { analyticsIdentityQuery, describeIdentityQuery, identityParams } from './identity-query';

describe('analyticsIdentityQuery', () => {
	it('parses a tag, a login, and a creator-style email', () => {
		expect(analyticsIdentityQuery('  tag:Prod ')).toEqual({ tag: 'prod' });
		expect(analyticsIdentityQuery('tag:')).toEqual({ tag: '*' });
		expect(analyticsIdentityQuery('user@Ada')).toEqual({ user: 'ada' });
		expect(analyticsIdentityQuery('user@')).toEqual({ user: '*' });
		expect(analyticsIdentityQuery('Ada@Example.com')).toEqual({ user: 'ada@example.com' });
		expect(analyticsIdentityQuery('build')).toEqual({ q: 'build' });
		expect(analyticsIdentityQuery('ip:100.64')).toBeNull();
		expect(analyticsIdentityQuery('   ')).toBeNull();
	});

	it('describes the filter and encodes it for the rankings URL', () => {
		expect(describeIdentityQuery({ tag: 'prod' })).toBe('tag:prod');
		expect(describeIdentityQuery({ user: 'ada@example.com' })).toBe('ada@example.com');
		expect(describeIdentityQuery({ q: 'build' })).toBe('“build”');
		expect(identityParams({ tag: 'prod' })).toBe('&tag=prod');
		expect(identityParams({ user: 'ada@example.com' })).toBe('&user=ada%40example.com');
		expect(identityParams(null)).toBe('');
	});
});
