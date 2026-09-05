// Runs a panel's targets against the Prometheus API and derives what the
// panel components render: visible series, stat values, the manual
// "last updated" stamp (from the hidden companion target, coverage-14) and
// the honest empty-state reason built from /readyz collectors (brief §7: no
// fake data; an empty series says which collector/source is missing).
import { api } from '$lib/api/client';
import type { Panel, Readyz, ReadyzCollector, SourceKind, Target } from '$lib/api/types.gen';
import type { ResolvedRange } from '$lib/timerange';
import {
	query,
	queryPath,
	queryRange,
	queryRangePath,
	sampleValue,
	legendName,
	type MatrixSeries,
	type PromData,
	type VectorSample
} from './prom';

export interface TargetResult {
	target: Target;
	kind: 'instant' | 'range';
	/** The request path (for the inspector). */
	path: string;
	data: PromData | null;
	error?: string;
	warnings: string[];
}

export interface PanelRun {
	panelId: string;
	range: ResolvedRange;
	results: TargetResult[];
	/** Range query behind a stat panel's sparkline when its value target is instant. */
	spark?: TargetResult;
	fetchedAt: number;
}

// ---- in-flight request dedup (several panels may ask for the same URL) ----
// One fetch per URL with its own AbortController; it is aborted only when every
// subscriber has aborted, so a refresh that supersedes an older one never
// surfaces the older run's AbortError in a panel.
interface Inflight {
	promise: Promise<PromData>;
	ctl: AbortController;
	subscribers: number;
}
const inflight = new Map<string, Inflight>();

function dedup(
	key: string,
	run: (signal: AbortSignal) => Promise<PromData>,
	signal?: AbortSignal
): Promise<PromData> {
	let entry = inflight.get(key);
	if (!entry) {
		const ctl = new AbortController();
		const e: Inflight = { ctl, subscribers: 0, promise: Promise.resolve() as never };
		e.promise = run(ctl.signal).finally(() => {
			if (inflight.get(key) === e) inflight.delete(key);
		});
		inflight.set(key, e);
		entry = e;
	}
	const mine = entry;
	mine.subscribers++;
	const onAbort = () => {
		mine.subscribers--;
		if (mine.subscribers <= 0) mine.ctl.abort();
	};
	if (signal?.aborted) onAbort();
	else signal?.addEventListener('abort', onAbort, { once: true });
	return mine.promise.finally(() => signal?.removeEventListener('abort', onAbort));
}

function errText(e: unknown): string {
	if (e instanceof Error) return e.message;
	return String(e);
}

async function runTarget(
	target: Target,
	range: ResolvedRange,
	signal: AbortSignal | undefined,
	forceRange = false
): Promise<TargetResult> {
	const instant = !!target.instant && !forceRange;
	const path = instant
		? queryPath(target.expr)
		: queryRangePath(target.expr, range.from, range.to, range.step);
	const base: TargetResult = {
		target,
		kind: instant ? 'instant' : 'range',
		path,
		data: null,
		warnings: []
	};
	try {
		let warnings: string[] = [];
		const data = await dedup(
			path,
			async (sig) => {
				const res = instant
					? await query(target.expr, { signal: sig })
					: await queryRange(target.expr, { ...range, signal: sig });
				warnings = res.warnings;
				return res.data;
			},
			signal
		);
		if (signal?.aborted) throw new DOMException('superseded', 'AbortError');
		return { ...base, data, warnings };
	} catch (e) {
		if (signal?.aborted) throw e;
		return { ...base, error: errText(e) };
	}
}

export type GraphMode = 'none' | 'area';
export type ColorMode = 'value' | 'background' | 'none';

function optionString(panel: Panel, key: string): string | undefined {
	const o = panel.options as Record<string, unknown> | undefined;
	const v = o?.[key];
	return typeof v === 'string' ? v : undefined;
}

export function graphMode(panel: Panel): GraphMode {
	return optionString(panel, 'graph_mode') === 'area' ? 'area' : 'none';
}

export function colorMode(panel: Panel): ColorMode {
	const v = optionString(panel, 'color_mode');
	return v === 'background' ? 'background' : v === 'value' ? 'value' : 'none';
}

export function showThresholdMarkers(panel: Panel): boolean {
	const o = panel.options as Record<string, unknown> | undefined;
	return o?.show_threshold_markers !== false;
}

/** Runs every target (hidden ones too — their latest value feeds the panel). */
export async function runPanel(
	panel: Panel,
	range: ResolvedRange,
	signal?: AbortSignal
): Promise<PanelRun> {
	const results = await Promise.all(panel.targets.map((t) => runTarget(t, range, signal)));
	let spark: TargetResult | undefined;
	if (panel.type === 'stat' && graphMode(panel) === 'area') {
		const first = panel.targets.find((t) => !t.hide);
		if (first?.instant) spark = await runTarget(first, range, signal, true);
	}
	return { panelId: panel.id, range, results, spark, fetchedAt: Date.now() };
}

export function visibleResults(run: PanelRun): TargetResult[] {
	return run.results.filter((r) => !r.target.hide);
}

export function firstError(run: PanelRun): string | undefined {
	return run.results.find((r) => r.error)?.error;
}

/** Latest numeric value of a result (vector: first sample; matrix: last point of the first series; scalar). */
export function latestValue(res: TargetResult | undefined): number | undefined {
	const d = res?.data;
	if (!d) return undefined;
	switch (d.resultType) {
		case 'vector':
			return d.result.length ? sampleValue(d.result[0]!.value[1]) : undefined;
		case 'matrix': {
			const s = d.result[0];
			if (!s || s.values.length === 0) return undefined;
			return sampleValue(s.values[s.values.length - 1]![1]);
		}
		case 'scalar':
			return sampleValue(d.result[1]);
		default:
			return undefined;
	}
}

/** True when the visible targets all answered with no series (the honest empty state). */
export function isEmpty(run: PanelRun): boolean {
	const vis = visibleResults(run);
	if (vis.length === 0) return true;
	return vis.every((r) => {
		if (r.error) return false;
		const d = r.data;
		if (!d) return true;
		if (d.resultType === 'vector' || d.resultType === 'matrix') {
			return d.result.length === 0 || d.result.every((s) => 'values' in s && s.values.length === 0);
		}
		return false;
	});
}

export interface StatValue {
	name: string;
	metric: Record<string, string>;
	value: number;
	/** Sparkline points [ts, value] when a range result backs the value. */
	points?: [number, number][];
}

/** The values a stat panel shows: one per series of the first visible target (last non-null for ranges). */
export function statValues(run: PanelRun): StatValue[] {
	const res = visibleResults(run)[0];
	const d = res?.data;
	if (!res || !d) return [];
	const fmt = res.target.legendFormat;
	if (d.resultType === 'vector') {
		return d.result.map((s: VectorSample) => ({
			name: legendName(fmt, s.metric),
			metric: s.metric,
			value: sampleValue(s.value[1])
		}));
	}
	if (d.resultType === 'matrix') {
		const out: StatValue[] = [];
		for (const s of d.result as MatrixSeries[]) {
			const pts = s.values.map(([t, v]) => [t, sampleValue(v)] as [number, number]);
			const last = [...pts].reverse().find((p) => !Number.isNaN(p[1]));
			if (last)
				out.push({
					name: legendName(fmt, s.metric),
					metric: s.metric,
					value: last[1],
					points: pts
				});
		}
		return out;
	}
	if (d.resultType === 'scalar') {
		return [{ name: 'scalar', metric: {}, value: sampleValue(d.result[1]) }];
	}
	return [];
}

/** Sparkline points for a stat panel (the spark run, else the value target's own range). */
export function sparkPoints(run: PanelRun): [number, number][] | undefined {
	const src = run.spark ?? visibleResults(run)[0];
	const d = src?.data;
	if (!d || d.resultType !== 'matrix' || d.result.length === 0) return undefined;
	const pts = d.result[0]!.values.map(([t, v]) => [t, sampleValue(v)] as [number, number]);
	return pts.length >= 2 ? pts : undefined;
}

/**
 * "last updated" for manual-source panels: the latest value of the hidden
 * companion target (unix seconds). `unknown` when that series is missing —
 * content/manual_metrics.yaml has `updated_at: TODO(divy)`.
 */
export function lastUpdated(panel: Panel, run: PanelRun): { ts?: number; unknown: boolean } {
	if (panel.source.kind !== 'manual') return { unknown: false };
	const companion = run.results.find(
		(r) =>
			r.target.hide ||
			(panel.source.updated_metric && r.target.expr === panel.source.updated_metric)
	);
	const v = latestValue(companion);
	if (v === undefined || !Number.isFinite(v) || v <= 0) return { unknown: true };
	return { ts: v, unknown: false };
}

// ---- collectors and empty-state reasons ----

/** Which collector fills a panel's source kind (null = live series computed by the API itself). */
export function sourceCollector(kind: SourceKind): string | null {
	switch (kind) {
		case 'github':
			return 'github';
		case 'pypi':
			return 'pypi';
		case 'manual':
			return 'manual';
		default:
			return null;
	}
}

/** Collector by metric-name prefix (Explore has no panel source to go by). */
export function collectorForMetric(name: string): string | null {
	if (name.startsWith('github_') || name === 'oss_prs_open') return 'github';
	if (name.startsWith('pypi_')) return 'pypi';
	if (name.startsWith('probe_')) return 'uptime';
	if (
		name === 'savely_active_users' ||
		name === 'lfx_applications' ||
		name.startsWith('divy_manual_')
	)
		return 'manual';
	return null;
}

export type CollectorState = 'missing' | 'disabled' | 'never' | 'stale' | 'ok';

export interface CollectorStatus {
	name: string;
	state: CollectorState;
	text: string;
	collector?: ReadyzCollector;
}

function ago(s: number): string {
	if (s < 90) return `${s}s ago`;
	if (s < 5400) return `${Math.round(s / 60)}m ago`;
	if (s < 172800) return `${Math.round(s / 3600)}h ago`;
	return `${Math.round(s / 86400)}d ago`;
}

export function collectorStatus(name: string, readyz: Readyz | null | undefined): CollectorStatus {
	const c = readyz?.checks.collectors[name];
	if (!c) {
		return {
			name,
			state: 'missing',
			text: readyz
				? `${name}: collector not registered in this build (nothing has written ${name} samples yet)`
				: `${name}: collector status unknown (/readyz unreachable)`
		};
	}
	if (c.disabled) {
		const why =
			name === 'github'
				? 'no DIVY_GITHUB_TOKEN configured'
				: name === 'uptime'
					? 'no targets configured'
					: 'not configured';
		return { name, state: 'disabled', text: `${name}: collector disabled — ${why}`, collector: c };
	}
	if (c.ok === null || c.last_success === null) {
		return { name, state: 'never', text: `${name}: no successful collector run yet`, collector: c };
	}
	const age = c.age_s ?? 0;
	if (!c.ok) {
		return {
			name,
			state: 'stale',
			text: `${name}: last successful run ${ago(age)} — stale (limit ${Math.round(c.stale_after_s / 60)}m)`,
			collector: c
		};
	}
	return { name, state: 'ok', text: `${name}: collector ran ${ago(age)}`, collector: c };
}

/** The sentence an empty panel/series shows. */
export function emptyMessage(kind: SourceKind, readyz: Readyz | null | undefined): string {
	const name = sourceCollector(kind);
	if (!name)
		return 'Live series (computed by the API at request time) returned no samples for this expression.';
	const st = collectorStatus(name, readyz);
	if (st.state === 'ok')
		return `No series matched. ${st.text}, so the query itself returned nothing.`;
	return `No data — ${st.text}.`;
}

/** Empty message for an arbitrary expression (Explore): guesses the collector from metric names in it. */
export function emptyMessageForExpr(expr: string, readyz: Readyz | null | undefined): string {
	const names = expr.match(/[A-Za-z_:][A-Za-z0-9_:]*/g) ?? [];
	const cols = new Set<string>();
	for (const n of names) {
		const c = collectorForMetric(n);
		if (c) cols.add(c);
	}
	if (cols.size === 0) return 'Empty result: no series matched this expression.';
	return 'Empty result — ' + [...cols].map((c) => collectorStatus(c, readyz).text).join('; ') + '.';
}

let readyzCache: { at: number; value: Readyz | null } | undefined;

/** /readyz, cached for 10 s (every panel asks for it on each refresh). */
export async function fetchReadyz(force = false): Promise<Readyz | null> {
	const now = Date.now();
	if (!force && readyzCache && now - readyzCache.at < 10_000) return readyzCache.value;
	try {
		const v = await api.readyz();
		readyzCache = { at: now, value: v };
		return v;
	} catch (e) {
		// 503 still carries the body; ApiError hides it — treat as unreachable
		void e;
		readyzCache = { at: now, value: null };
		return null;
	}
}
