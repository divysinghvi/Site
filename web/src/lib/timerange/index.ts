// Time range presets for the dashboard and Explore (brief §3.2: 24h / 7d / 30d
// / 1y / all) and the step the range queries use. The presets and the default
// come from content/panels.yaml (dashboard.time); `all` starts at the career
// root span's resolved start, which the page passes in.
import type { TimeOption } from '$lib/api/types.gen';

export type Preset = TimeOption;

const S = 1;
const MIN = 60 * S;
const H = 60 * MIN;
const D = 24 * H;

/** Seconds covered by each fixed preset. */
export const PRESET_SECONDS: Record<Exclude<Preset, 'all'>, number> = {
	'24h': D,
	'7d': 7 * D,
	'30d': 30 * D,
	'1y': 365 * D
};

/** Human labels for the picker. */
export const PRESET_LABELS: Record<Preset, string> = {
	'24h': 'Last 24 hours',
	'7d': 'Last 7 days',
	'30d': 'Last 30 days',
	'1y': 'Last year',
	all: 'All time'
};

export const ALL_PRESETS: readonly Preset[] = ['24h', '7d', '30d', '1y', 'all'];

export function isPreset(v: string | null | undefined): v is Preset {
	return v === '24h' || v === '7d' || v === '30d' || v === '1y' || v === 'all';
}

/**
 * The floor step per preset (docs/drafts/promql.md P.11: 24h → 5m, 7d → 1h,
 * 30d → 6h, 1y → 1d, all → 1d). Stored counters are one sample per UTC day,
 * so anything finer than a day only repeats values.
 */
export const PRESET_MIN_STEP: Record<Preset, number> = {
	'24h': 5 * MIN,
	'7d': H,
	'30d': 6 * H,
	'1y': D,
	all: D
};

/** Candidate steps, ascending; the picker never emits anything else. */
const STEPS = [
	MIN,
	5 * MIN,
	15 * MIN,
	30 * MIN,
	H,
	2 * H,
	6 * H,
	12 * H,
	D,
	2 * D,
	7 * D,
	14 * D,
	30 * D
];

/** Hard cap on points per series in one range query (the API allows 11 000). */
export const MAX_POINTS = 1000;

/**
 * Picks the smallest candidate step ≥ `floor` that keeps (to − from) / step
 * ≤ MAX_POINTS. Always returns a whole number of seconds.
 */
export function chooseStep(fromS: number, toS: number, floor = 0): number {
	const span = Math.max(1, toS - fromS);
	for (const s of STEPS) {
		if (s < floor) continue;
		if (span / s <= MAX_POINTS) return s;
	}
	// beyond the table: a multiple of a day that fits
	return Math.ceil(span / MAX_POINTS / D) * D;
}

export interface ResolvedRange {
	preset: Preset;
	/** Unix seconds, inclusive. */
	from: number;
	/** Unix seconds (= now for presets). */
	to: number;
	/** Query step in seconds. */
	step: number;
}

export interface ResolveOptions {
	/** Unix ms "now"; defaults to Date.now(). */
	nowMs?: number;
	/** Unix seconds of the earliest instant `all` should cover (root span start). */
	allFrom?: number;
}

/** Resolves a preset into start/end/step at `now`. `all` without `allFrom` falls back to 1y. */
export function resolveRange(preset: Preset, opts: ResolveOptions = {}): ResolvedRange {
	const to = Math.floor((opts.nowMs ?? Date.now()) / 1000);
	let from: number;
	if (preset === 'all') {
		from = opts.allFrom !== undefined ? Math.floor(opts.allFrom) : to - PRESET_SECONDS['1y'];
		if (from >= to) from = to - D;
	} else {
		from = to - PRESET_SECONDS[preset];
	}
	const step = chooseStep(from, to, PRESET_MIN_STEP[preset]);
	return { preset, from, to, step };
}

/** Relative time expressions for links: `now-7d` / `now` (Grafana's form). */
export function presetToRelative(preset: Preset, allFrom?: number): { from: string; to: string } {
	if (preset === 'all')
		return { from: allFrom !== undefined ? String(allFrom) : 'now-1y', to: 'now' };
	return { from: `now-${preset}`, to: 'now' };
}

const REL = /^now(?:-(\d+)([smhdwy]))?$/;
const UNIT_S: Record<string, number> = { s: S, m: MIN, h: H, d: D, w: 7 * D, y: 365 * D };

/**
 * Parses a `from`/`to` query parameter: `now`, `now-7d`, unix seconds, unix
 * milliseconds (> 1e11) or an ISO-8601 date. Returns unix seconds or undefined.
 */
export function parseTimeParam(
	v: string | null | undefined,
	nowMs = Date.now()
): number | undefined {
	if (!v) return undefined;
	const s = v.trim();
	const m = REL.exec(s);
	if (m) {
		const now = Math.floor(nowMs / 1000);
		if (!m[1]) return now;
		return now - Number(m[1]) * (UNIT_S[m[2]!] ?? 0);
	}
	if (/^\d+(\.\d+)?$/.test(s)) {
		const n = Number(s);
		return n > 1e11 ? Math.floor(n / 1000) : Math.floor(n);
	}
	const t = Date.parse(s);
	return Number.isFinite(t) ? Math.floor(t / 1000) : undefined;
}

/** Maps a relative `from`/`to` pair back to a preset when it is one of ours. */
export function presetFromParams(from: string | null, to: string | null): Preset | undefined {
	if (to && to !== 'now') return undefined;
	if (!from) return undefined;
	const m = /^now-(24h|7d|30d|1y)$/.exec(from.trim());
	if (m) return m[1] as Preset;
	return undefined;
}

/** Formats a step for `step=` (whole seconds are what the API expects). */
export function formatStep(step: number): string {
	return String(Math.max(1, Math.round(step)));
}

/** Human step: 5m, 1h, 1d. */
export function humanStep(step: number): string {
	if (step % D === 0) return `${step / D}d`;
	if (step % H === 0) return `${step / H}h`;
	if (step % MIN === 0) return `${step / MIN}m`;
	return `${step}s`;
}
