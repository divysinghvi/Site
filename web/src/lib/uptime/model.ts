// Uptime page model: 90-day day cells from /api/uptime buckets (absent days
// stay `null` → grey, never green), cell colouring by up_ratio, the honest
// overall status, and small formatters. Nothing here invents a probe.
import type { HeartbeatBucket, HeartbeatTarget, UptimeTargetView } from '$lib/api/types.gen';

export const DAY_MS = 86_400_000;

export interface DayCell {
	/** YYYY-MM-DD (UTC) */
	key: string;
	/** start of the UTC day, ms */
	ts: number;
	bucket: HeartbeatBucket | null;
}

/** One cell per UTC day, oldest first, ending on the day that contains `nowMs`. */
export function dayCells(
	buckets: readonly HeartbeatBucket[],
	days: number,
	nowMs: number
): DayCell[] {
	const byKey = new Map<string, HeartbeatBucket>();
	for (const b of buckets) byKey.set(b.ts.slice(0, 10), b);
	const d = new Date(nowMs);
	const today = Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate());
	const out: DayCell[] = [];
	for (let i = days - 1; i >= 0; i--) {
		const ts = today - i * DAY_MS;
		const key = new Date(ts).toISOString().slice(0, 10);
		out.push({ key, ts, bucket: byKey.get(key) ?? null });
	}
	return out;
}

export type CellState = 'up' | 'partial' | 'down' | 'none';

/** Colour class of a day: green only when every probe of the day succeeded. */
export function cellState(b: HeartbeatBucket | null): CellState {
	if (!b || b.samples <= 0) return 'none';
	if (b.up_ratio >= 0.9995) return 'up';
	if (b.up_ratio <= 0) return 'down';
	return 'partial';
}

export function cellTitle(c: DayCell): string {
	if (!c.bucket || c.bucket.samples <= 0) return `${c.key}: no probes`;
	const b = c.bucket;
	return `${c.key}: ${b.samples} probe${b.samples === 1 ? '' : 's'}, ${formatPct(b.up_ratio, 1)} up, avg ${formatLatency(b.avg_latency_ms)}`;
}

export interface CellSummary {
	up: number;
	partial: number;
	down: number;
	none: number;
}

export function summarizeCells(cells: readonly DayCell[]): CellSummary {
	const s: CellSummary = { up: 0, partial: 0, down: 0, none: 0 };
	for (const c of cells) s[cellState(c.bucket)]++;
	return s;
}

/** "99.97%" or "—" (null = no probes in that window; never a fake 100%). */
export function formatPct(ratio: number | null | undefined, digits = 2): string {
	if (ratio === null || ratio === undefined || !Number.isFinite(ratio)) return '—';
	return `${(ratio * 100).toFixed(digits)}%`;
}

export function formatLatency(ms: number | null | undefined): string {
	if (ms === null || ms === undefined || !Number.isFinite(ms)) return '—';
	if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`;
	if (ms >= 100) return `${Math.round(ms)} ms`;
	if (ms >= 10) return `${ms.toFixed(1)} ms`;
	return `${ms.toFixed(2)} ms`;
}

/** Whole seconds → "45s" · "15m" · "2h 05m" · "3d 4h". */
export function formatSeconds(s: number): string {
	if (!Number.isFinite(s) || s < 0) return '—';
	const r = Math.round(s);
	if (r < 60) return `${r}s`;
	if (r < 3600)
		return `${Math.floor(r / 60)}m${r % 60 ? ` ${String(r % 60).padStart(2, '0')}s` : ''}`;
	if (r < 86400) {
		const h = Math.floor(r / 3600);
		const m = Math.floor((r % 3600) / 60);
		return `${h}h${m ? ` ${String(m).padStart(2, '0')}m` : ''}`;
	}
	const d = Math.floor(r / 86400);
	const h = Math.floor((r % 86400) / 3600);
	return `${d}d${h ? ` ${h}h` : ''}`;
}

/** "32 s ago" · "5 min ago" · "3 h ago" · "2 d ago"; "in the future" is clamped to "just now". */
export function formatAgo(iso: string, nowMs: number): string {
	const t = Date.parse(iso);
	if (!Number.isFinite(t)) return iso;
	const s = Math.max(0, Math.round((nowMs - t) / 1000));
	if (s < 5) return 'just now';
	if (s < 90) return `${s} s ago`;
	if (s < 5400) return `${Math.round(s / 60)} min ago`;
	if (s < 172_800) return `${Math.round(s / 3600)} h ago`;
	return `${Math.round(s / 86400)} d ago`;
}

/** ISO → "2026-09-05 11:00:32Z" (UTC, seconds). */
export function formatUtc(iso: string): string {
	const t = Date.parse(iso);
	if (!Number.isFinite(t)) return iso;
	return new Date(t).toISOString().slice(0, 19).replace('T', ' ') + 'Z';
}

export const ERROR_CLASSES = [
	'dns',
	'tls',
	'timeout',
	'conn',
	'http',
	'redirect',
	'read',
	'other'
] as const;
export type ErrorClass = (typeof ERROR_CLASSES)[number];

/** The prober stores errors as "<class>: <message>". */
export function errorClass(err: string | null | undefined): ErrorClass | null {
	if (!err) return null;
	const i = err.indexOf(':');
	const head = (i >= 0 ? err.slice(0, i) : err).trim() as ErrorClass;
	return (ERROR_CLASSES as readonly string[]).includes(head) ? head : 'other';
}

export function errorMessage(err: string | null | undefined): string {
	if (!err) return '';
	const i = err.indexOf(':');
	return i >= 0 ? err.slice(i + 1).trim() : err;
}

export type OverallLevel = 'ok' | 'degraded' | 'partial' | 'unknown' | 'none';

export interface Overall {
	level: OverallLevel;
	/** Short headline (no numbers baked into UI copy beyond the counts). */
	text: string;
	up: HeartbeatTarget[];
	down: HeartbeatTarget[];
	unknown: HeartbeatTarget[];
	unconfigured: HeartbeatTarget[];
	monitored: number;
}

/**
 * The status banner. Unconfigured targets (TODO url) are excluded from the
 * verdict and listed separately; a monitored target with no probe yet is
 * "unknown", never up.
 */
export function overallStatus(targets: readonly HeartbeatTarget[]): Overall {
	const up = targets.filter((t) => t.status === 'up');
	const down = targets.filter((t) => t.status === 'down');
	const unknown = targets.filter((t) => t.status === 'unknown');
	const unconfigured = targets.filter((t) => t.status === 'unconfigured');
	const monitored = up.length + down.length + unknown.length;
	let level: OverallLevel;
	let text: string;
	if (monitored === 0) {
		level = 'none';
		text = 'No monitored targets';
	} else if (down.length > 0) {
		level = 'degraded';
		text = `Degraded — ${down.length} of ${monitored} monitored target${monitored === 1 ? '' : 's'} down`;
	} else if (unknown.length === monitored) {
		level = 'unknown';
		text = 'No probes yet';
	} else if (unknown.length > 0) {
		level = 'partial';
		text = `${up.length} of ${monitored} up — ${unknown.length} awaiting a first probe`;
	} else {
		level = 'ok';
		text = 'All monitored targets operational';
	}
	return { level, text, up, down, unknown, unconfigured, monitored };
}

/** Joins the content view (method, interval, timeout, expected_status) onto a heartbeat target by id. */
export function contentFor(
	views: readonly UptimeTargetView[] | undefined,
	target: string
): UptimeTargetView | undefined {
	return views?.find((v) => v.id === target);
}
