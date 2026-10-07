import { ALL_LIVE_MS, MIN_WINDOW_MS, formatWindow } from './time-window';

export type TimeZoneMode = 'local' | 'utc';

export type RangeIntent =
	| { kind: 'sliding'; windowMs: number }
	| { kind: 'all' }
	| { kind: 'since'; start: Date }
	| { kind: 'absolute'; start: Date; end: Date };

export type ParseResult = { ok: true; intent: RangeIntent } | { ok: false; message: string };

export type DecodedSearch =
	| { status: 'missing' }
	| { status: 'invalid'; message: string }
	| { status: 'ok'; intent: RangeIntent };

const YEAR_MS = 365 * 24 * 60 * 60 * 1000;

const RELATIVE =
	/^(?:last\s+|past\s+|now-)?(\d+(?:\.\d+)?)\s*(minutes?|mins?|hours?|hrs?|days?|weeks?|[mhdw])$/i;

const CLOCK = /^(\d{1,2})(?::(\d{2}))?\s*(am|pm)?$/i;

export function rangeSignature(search: string): string {
	const params = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search);
	return [params.get('from') ?? '', params.get('to') ?? '', params.get('start') ?? '', params.get('end') ?? ''].join('|');
}

export function formatRelativeToken(ms: number): string {
	const minute = 60_000;
	const hour = 60 * minute;
	const day = 24 * hour;
	const week = 7 * day;
	if (ms % week === 0 && ms >= week) return `${ms / week}w`;
	if (ms % day === 0 && ms >= day) return `${ms / day}d`;
	if (ms % hour === 0 && ms >= hour) return `${ms / hour}h`;
	return `${Math.max(1, Math.round(ms / minute))}m`;
}

export function encodeIntent(intent: RangeIntent): { from: string; to: string } {
	if (intent.kind === 'all') return { from: 'all', to: 'now' };
	if (intent.kind === 'sliding') return { from: `now-${formatRelativeToken(intent.windowMs)}`, to: 'now' };
	if (intent.kind === 'since') return { from: intent.start.toISOString(), to: 'now' };
	return { from: intent.start.toISOString(), to: intent.end.toISOString() };
}

export function intentFromParts(input: {
	live: boolean;
	windowMs: number;
	anchoredStart: Date | null;
	start: Date | null;
	end: Date | null;
}): RangeIntent {
	if (input.live && input.windowMs >= YEAR_MS) return { kind: 'all' };
	if (input.live && input.anchoredStart) return { kind: 'since', start: input.anchoredStart };
	if (input.live) return { kind: 'sliding', windowMs: Math.max(MIN_WINDOW_MS, input.windowMs || ALL_LIVE_MS) };
	const start = input.start ?? new Date();
	const end = input.end ?? new Date(start.getTime() + MIN_WINDOW_MS);
	return { kind: 'absolute', start, end };
}

export function decodeSearch(params: URLSearchParams, now = new Date()): DecodedSearch {
	const from = params.get('from');
	const to = params.get('to');
	if (from !== null || to !== null) return decodePair(from ?? '', to ?? '', now, false);
	const start = params.get('start');
	const end = params.get('end');
	if (start !== null || end !== null) {
		if (!start || !end) return { status: 'invalid', message: 'A start and end are both required.' };
		return decodePair(start, end, now, true);
	}
	return { status: 'missing' };
}

export function parseRangeInput(raw: string, now: Date, zone: TimeZoneMode = 'local'): ParseResult {
	const text = raw.trim().replace(/\s+/g, ' ');
	if (!text) return { ok: false, message: 'Enter a range.' };
	if (/^all$/i.test(text)) return { ok: true, intent: { kind: 'all' } };

	const relative = parseRelative(text);
	if (relative !== null) {
		if (relative < MIN_WINDOW_MS) return { ok: false, message: 'Use at least 5 minutes.' };
		return { ok: true, intent: { kind: 'sliding', windowMs: relative } };
	}

	const since = text.match(/^since\s+(.+)$/i);
	if (since) {
		const start = parseInstant(since[1], now, zone, true);
		if (!start) return { ok: false, message: "Couldn't read that start time." };
		return { ok: true, intent: { kind: 'since', start: rollClock(start, now, isClock(since[1])) } };
	}

	const pair = splitPair(text);
	if (pair) {
		const start = parseInstant(pair[0], now, zone, true);
		const end = parseInstant(pair[1], now, zone, true);
		if (!start || !end) return { ok: false, message: "Couldn't read that range." };
		let endMs = end.getTime();
		if (endMs <= start.getTime() && isClock(pair[0]) && isClock(pair[1])) endMs += 24 * 60 * 60 * 1000;
		if (endMs <= start.getTime()) return { ok: false, message: 'End must be after start.' };
		if (endMs - start.getTime() < MIN_WINDOW_MS) return { ok: false, message: 'Use at least 5 minutes.' };
		return { ok: true, intent: { kind: 'absolute', start, end: new Date(endMs) } };
	}

	const single = parseInstant(text, now, zone, false);
	if (single) return { ok: true, intent: { kind: 'since', start: single } };
	return { ok: false, message: "Couldn't read that range." };
}

export function intentLabel(intent: RangeIntent, zone: TimeZoneMode = 'local'): string {
	if (intent.kind === 'all') return 'All';
	if (intent.kind === 'sliding') return `Last ${formatWindow(intent.windowMs)}`;
	if (intent.kind === 'since') return `Since ${formatClock(intent.start, zone)}`;
	return `${formatStamp(intent.start, zone)} – ${formatStamp(intent.end, zone)}`;
}

export function formatClock(date: Date, zone: TimeZoneMode): string {
	const hours = zone === 'utc' ? date.getUTCHours() : date.getHours();
	const minutes = zone === 'utc' ? date.getUTCMinutes() : date.getMinutes();
	const suffix = hours >= 12 ? 'PM' : 'AM';
	const hour12 = hours % 12 === 0 ? 12 : hours % 12;
	return `${hour12}:${String(minutes).padStart(2, '0')} ${suffix}`;
}

export function formatStamp(date: Date, zone: TimeZoneMode): string {
	const month = zone === 'utc' ? date.getUTCMonth() : date.getMonth();
	const day = zone === 'utc' ? date.getUTCDate() : date.getDate();
	const names = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
	return `${names[month]} ${day}, ${formatClock(date, zone)}`;
}

const RECENT_KEY = 'tsflow-recent-ranges';
const ZONE_KEY = 'tsflow-time-zone';
const COLLAPSE_KEY = 'tsflow-timeline-collapsed';

export interface RecentRange {
	from: string;
	to: string;
	label: string;
}

export function loadRecent(): RecentRange[] {
	return readJson<RecentRange[]>(RECENT_KEY)?.filter((item) => item?.from && item?.to && item?.label).slice(0, 6) ?? [];
}

export function rememberRange(intent: RangeIntent, zone: TimeZoneMode): RecentRange[] {
	const encoded = encodeIntent(intent);
	const next: RecentRange = { ...encoded, label: intentLabel(intent, zone) };
	const items = [next, ...loadRecent().filter((item) => item.from !== next.from || item.to !== next.to)].slice(0, 6);
	writeJson(RECENT_KEY, items);
	return items;
}

export function loadTimeZone(): TimeZoneMode {
	const stored = readRaw(ZONE_KEY);
	return stored === 'utc' ? 'utc' : 'local';
}

export function saveTimeZone(zone: TimeZoneMode) {
	writeRaw(ZONE_KEY, zone);
}

export function loadTimelineCollapsed(): boolean {
	return readRaw(COLLAPSE_KEY) === '1';
}

export function saveTimelineCollapsed(collapsed: boolean) {
	writeRaw(COLLAPSE_KEY, collapsed ? '1' : '0');
}

function decodePair(from: string, to: string, now: Date, legacy: boolean): DecodedSearch {
	if (!from || !to) return { status: 'invalid', message: 'A from and to are both required.' };
	if (!legacy && to === 'now') {
		if (from === 'all') return { status: 'ok', intent: { kind: 'all' } };
		const relative = parseRelative(from);
		if (relative !== null) {
			if (relative < MIN_WINDOW_MS) return { status: 'invalid', message: 'Use at least 5 minutes.' };
			return { status: 'ok', intent: { kind: 'sliding', windowMs: relative } };
		}
		const start = parseInstant(from, now, 'utc', false);
		if (!start) return { status: 'invalid', message: "Couldn't read that start time." };
		return { status: 'ok', intent: { kind: 'since', start } };
	}
	const start = parseRelativeInstant(from, now) ?? parseInstant(from, now, 'utc', false);
	const end = parseRelativeInstant(to, now) ?? parseInstant(to, now, 'utc', false);
	if (!start || !end || end.getTime() <= start.getTime()) {
		return { status: 'invalid', message: "Couldn't read that range." };
	}
	return { status: 'ok', intent: { kind: 'absolute', start, end } };
}

function parseRelativeInstant(text: string, now: Date): Date | null {
	if (text === 'now') return now;
	const relative = parseRelative(text);
	if (relative === null) return null;
	return new Date(now.getTime() - relative);
}

function parseRelative(text: string): number | null {
	const match = text.trim().match(RELATIVE);
	if (!match) return null;
	const amount = Number(match[1]);
	if (!Number.isFinite(amount) || amount <= 0) return null;
	const unit = match[2].toLowerCase();
	const minute = 60_000;
	const hour = 60 * minute;
	const day = 24 * hour;
	if (unit.startsWith('w')) return Math.round(amount * 7 * day);
	if (unit.startsWith('d')) return Math.round(amount * day);
	if (unit.startsWith('h')) return Math.round(amount * hour);
	return Math.round(amount * minute);
}

function splitPair(text: string): [string, string] | null {
	const words = text.split(/\s+(?:to|–|—)\s+/i);
	if (words.length === 2) return [words[0], words[1]];
	const clock = text.match(/^(.+?\s*(?:am|pm)?)\s*[-–—]\s*(.+)$/i);
	if (clock && isClock(clock[1]) && isClock(clock[2])) return [clock[1].trim(), clock[2].trim()];
	const unix = text.match(/^(\d{10,13})\s*[-–—]\s*(\d{10,13})$/);
	if (unix) return [unix[1], unix[2]];
	const iso = text.match(
		/^(\d{4}-\d{2}-\d{2}(?:[T ][0-9:.]+(?:Z|[+-]\d{2}:?\d{2})?)?)\s+(?:to\s+)?(\d{4}-\d{2}-\d{2}(?:[T ][0-9:.]+(?:Z|[+-]\d{2}:?\d{2})?)?)$/i
	);
	if (iso) return [iso[1], iso[2]];
	return null;
}

function parseInstant(text: string, now: Date, zone: TimeZoneMode, allowClock: boolean): Date | null {
	const trimmed = text.trim();
	if (/^\d{10,13}$/.test(trimmed)) {
		const value = Number(trimmed);
		const ms = trimmed.length <= 10 ? value * 1000 : value;
		const date = new Date(ms);
		return Number.isNaN(date.getTime()) ? null : date;
	}
	if (allowClock && isClock(trimmed)) {
		const clock = parseClock(trimmed);
		if (!clock) return null;
		const parts = zoneParts(now, zone);
		return makeZoned(parts.year, parts.month, parts.day, clock.hour, clock.minute, zone);
	}
	if (/^\d{4}-\d{2}-\d{2}$/.test(trimmed)) {
		const [year, month, day] = trimmed.split('-').map(Number);
		return makeZoned(year, month - 1, day, 0, 0, zone);
	}
	const parsed = new Date(trimmed);
	return Number.isNaN(parsed.getTime()) ? null : parsed;
}

function isClock(text: string): boolean {
	return CLOCK.test(text.trim());
}

function parseClock(text: string): { hour: number; minute: number } | null {
	const match = text.trim().match(CLOCK);
	if (!match) return null;
	let hour = Number(match[1]);
	const minute = match[2] ? Number(match[2]) : 0;
	const suffix = match[3]?.toLowerCase();
	if (minute > 59 || hour > 23) return null;
	if (suffix) {
		if (hour < 1 || hour > 12) return null;
		if (suffix === 'pm' && hour < 12) hour += 12;
		if (suffix === 'am' && hour === 12) hour = 0;
	}
	if (hour > 23) return null;
	return { hour, minute };
}

function rollClock(start: Date, now: Date, clock: boolean): Date {
	if (!clock || start.getTime() <= now.getTime()) return start;
	return new Date(start.getTime() - 24 * 60 * 60 * 1000);
}

function zoneParts(now: Date, zone: TimeZoneMode): { year: number; month: number; day: number } {
	if (zone === 'utc') {
		return { year: now.getUTCFullYear(), month: now.getUTCMonth(), day: now.getUTCDate() };
	}
	return { year: now.getFullYear(), month: now.getMonth(), day: now.getDate() };
}

function makeZoned(year: number, month: number, day: number, hour: number, minute: number, zone: TimeZoneMode): Date {
	if (zone === 'utc') return new Date(Date.UTC(year, month, day, hour, minute, 0, 0));
	return new Date(year, month, day, hour, minute, 0, 0);
}

function readRaw(key: string): string | null {
	if (typeof localStorage === 'undefined') return null;
	try {
		return localStorage.getItem(key);
	} catch {
		return null;
	}
}

function writeRaw(key: string, value: string) {
	if (typeof localStorage === 'undefined') return;
	try {
		localStorage.setItem(key, value);
	} catch {
		/* ignore quota */
	}
}

function readJson<T>(key: string): T | null {
	const raw = readRaw(key);
	if (!raw) return null;
	try {
		return JSON.parse(raw) as T;
	} catch {
		return null;
	}
}

function writeJson(key: string, value: unknown) {
	writeRaw(key, JSON.stringify(value));
}
