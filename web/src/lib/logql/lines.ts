// Log rows for the logs page and Explore: the streams of a query_range
// response flattened into one newest-first list, each row carrying the parsed
// JSON of its line (content/logs.ndjson lines are JSON objects), the level /
// service / component labels, the span link and the human timestamp. Also the
// log-volume histogram helpers (sum by (level) (count_over_time(q[step]))).
import type { LokiMatrixSeries, Stream } from './loki';
import { lokiValue } from './loki';
import { LEVELS, type Level } from './selector';

/** Default `limit` of the logs page (the API's own default; max 5000). */
export const DEFAULT_LIMIT = 100;

export interface LogRow {
	/** Nanosecond timestamp (decimal string) — unique per entry, the row key. */
	ts: string;
	/** Millisecond timestamp for display. */
	tsMs: number;
	/** The raw line as stored. */
	line: string;
	/** The entry's final label set (stream labels + `| json` extractions). */
	labels: Record<string, string>;
	/** The parsed line, or null when it is not a JSON object. */
	json: Record<string, unknown> | null;
	level: string;
	service: string;
	component?: string;
	msg: string;
	/** Span id from the line (`span` field) — links to /trace/career?span=<id>. */
	span?: string;
	/** `precision` field of the line: day (default) | month | year. */
	precision: 'day' | 'month' | 'year';
	/** True when the line's `ts` is TODO(divy): the timestamp shown is the linked span's start. */
	tsTodo: boolean;
}

const NS_PER_MS = 1_000_000n;

/** Millisecond value of a nanosecond decimal string (BigInt: 19 digits exceed 2^53). */
export function nsToMs(ns: string): number {
	try {
		return Number(BigInt(ns) / NS_PER_MS);
	} catch {
		return NaN;
	}
}

function compareNsDesc(a: string, b: string): number {
	if (a.length !== b.length) return b.length - a.length;
	return a < b ? 1 : a > b ? -1 : 0;
}

function str(v: unknown): string | undefined {
	return typeof v === 'string' ? v : undefined;
}

function parseLine(line: string): Record<string, unknown> | null {
	try {
		const v: unknown = JSON.parse(line);
		return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null;
	} catch {
		return null;
	}
}

/** Flattens streams into rows sorted newest first (ties: file order via the ns index). */
export function rowsFromStreams(streams: readonly Stream[]): LogRow[] {
	const rows: LogRow[] = [];
	for (const s of streams) {
		for (const [ts, line] of s.values) {
			const json = parseLine(line);
			const level = s.stream.level ?? str(json?.level) ?? '';
			const service = s.stream.service ?? str(json?.service) ?? '';
			const component = s.stream.component ?? str(json?.component);
			const precision = str(json?.precision);
			const rawTs = str(json?.ts);
			rows.push({
				ts,
				tsMs: nsToMs(ts),
				line,
				labels: s.stream,
				json,
				level,
				service,
				component: component || undefined,
				msg: str(json?.msg) ?? line,
				span: str(json?.span) || undefined,
				precision: precision === 'month' || precision === 'year' ? precision : 'day',
				tsTodo: rawTs !== undefined && rawTs.startsWith('TODO(divy)')
			});
		}
	}
	rows.sort((a, b) => compareNsDesc(a.ts, b.ts));
	return rows;
}

/** Deep link to the span in the career trace (W3's form; the viewer also reads #span=). */
export function spanHref(span: string): string {
	return `/trace/career?span=${encodeURIComponent(span)}`;
}

/** CSS custom property for a level (Loki's convention: error red, warn yellow, info green, debug blue). */
export function levelVar(level: string): string {
	switch (level) {
		case 'error':
		case 'critical':
		case 'fatal':
			return 'var(--red)';
		case 'warn':
		case 'warning':
			return 'var(--yellow)';
		case 'info':
			return 'var(--green)';
		case 'debug':
		case 'trace':
			return 'var(--blue)';
		default:
			return 'var(--fg-dim)';
	}
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const pad = (n: number) => String(n).padStart(2, '0');

/** Loki-style timestamp column: 2026-03-01 00:00:00.000 (UTC). */
export function formatTs(ms: number): string {
	if (!Number.isFinite(ms)) return '—';
	const d = new Date(ms);
	return (
		`${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())} ` +
		`${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())}.${String(d.getUTCMilliseconds()).padStart(3, '0')}`
	);
}

/** The date at the line's own precision: "2023", "Mar 2026", "14 May 2024". */
export function formatPrecise(ms: number, precision: LogRow['precision']): string {
	if (!Number.isFinite(ms)) return '—';
	const d = new Date(ms);
	if (precision === 'year') return String(d.getUTCFullYear());
	if (precision === 'month') return `${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`;
	return `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`;
}

/** Stream labels the content always sets; everything else in `labels` came from `| json`. */
export const STREAM_LABELS = ['service', 'level', 'component'] as const;

export function extractedLabels(row: LogRow): [string, string][] {
	return Object.entries(row.labels)
		.filter(([k]) => !(STREAM_LABELS as readonly string[]).includes(k))
		.sort(([a], [b]) => a.localeCompare(b));
}

/** The line's own fields (what `| json` would extract), in file order. */
export function lineFields(row: LogRow): [string, string][] {
	if (!row.json) return [];
	return Object.entries(row.json).map(([k, v]) => [
		k,
		typeof v === 'string' ? v : JSON.stringify(v)
	]);
}

export function prettyJSON(row: LogRow): string {
	return row.json ? JSON.stringify(row.json, null, 2) : row.line;
}

/** JSON keys seen across rows (autocomplete source for label filters after `| json`). */
export function fieldNames(rows: readonly LogRow[]): string[] {
	const seen = new Set<string>();
	for (const r of rows) for (const k of Object.keys(r.json ?? {})) seen.add(k);
	return [...seen].sort();
}

// ---- log volume histogram ----

/** Bucket widths for the histogram: (duration literal, seconds), ascending. */
const BUCKETS: [string, number][] = [
	['1h', 3600],
	['3h', 10800],
	['6h', 21600],
	['12h', 43200],
	['1d', 86400],
	['2d', 172800],
	['7d', 604800],
	['14d', 1209600],
	['30d', 2592000],
	['60d', 5184000],
	['90d', 7776000],
	['180d', 15552000],
	['365d', 31536000]
];

/** The bucket (LogQL duration + seconds) that gives ≤ `max` bars over the range. */
export function volumeBucket(fromS: number, toS: number, max = 80): { dur: string; step: number } {
	const span = Math.max(1, toS - fromS);
	for (const [dur, step] of BUCKETS) if (span / step <= max) return { dur, step };
	const last = BUCKETS[BUCKETS.length - 1]!;
	return { dur: last[0], step: last[1] };
}

/** The volume query for a log query: sum by (level) (count_over_time(<q>[<bucket>])). */
export function volumeQuery(logQuery: string, dur: string): string {
	return `sum by (level) (count_over_time(${logQuery.trim()} [${dur}]))`;
}

export interface VolumeBar {
	/** Bucket end (unix seconds) — the step timestamp. */
	ts: number;
	counts: Record<string, number>;
	total: number;
}

export interface Volume {
	bars: VolumeBar[];
	totals: Record<string, number>;
	levels: string[];
	/** Bucket width in seconds. */
	step: number;
}

/** Turns the matrix of the volume query into contiguous bars over [from, to]. */
export function volumeFromMatrix(
	series: readonly LokiMatrixSeries[],
	fromS: number,
	toS: number,
	step: number
): Volume {
	const byTs = new Map<number, VolumeBar>();
	const totals: Record<string, number> = {};
	const levelSet = new Set<string>();
	for (let t = fromS; t <= toS; t += step) byTs.set(t, { ts: t, counts: {}, total: 0 });
	for (const s of series) {
		const level = s.metric.level ?? 'unknown';
		levelSet.add(level);
		for (const [t, v] of s.values) {
			const n = lokiValue(v);
			if (!Number.isFinite(n)) continue;
			let bar = byTs.get(t);
			if (!bar) {
				bar = { ts: t, counts: {}, total: 0 };
				byTs.set(t, bar);
			}
			bar.counts[level] = (bar.counts[level] ?? 0) + n;
			bar.total += n;
			totals[level] = (totals[level] ?? 0) + n;
		}
	}
	const known = LEVELS.filter((l) => levelSet.has(l)) as string[];
	const others = [...levelSet].filter((l) => !(LEVELS as readonly string[]).includes(l)).sort();
	return {
		bars: [...byTs.values()].sort((a, b) => a.ts - b.ts),
		totals,
		levels: [...known, ...others],
		step
	};
}

export function isLevel(v: string): v is Level {
	return (LEVELS as readonly string[]).includes(v);
}
