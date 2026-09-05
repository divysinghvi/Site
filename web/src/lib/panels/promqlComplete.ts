// PromQL autocomplete for Explore: metric names (from /api/v1/label/__name__/values),
// label names/values (from /api/v1/labels, /api/v1/label/{name}/values) and the
// static function/aggregation list that mirrors docs/promql-subset.md §1.
import type { MetricMeta } from './prom';

export type CompletionKind = 'metric' | 'function' | 'aggregation' | 'keyword' | 'label' | 'value';

export interface Completion {
	label: string;
	kind: CompletionKind;
	detail?: string;
	/** Text inserted in place of the prefix. */
	insert: string;
	/** Caret offset from the end of `insert` (negative = move back). */
	caretShift?: number;
}

/** Aggregation operators of the subset (docs/promql-subset.md §1). */
export const AGGREGATIONS: { name: string; detail: string }[] = [
	{ name: 'sum', detail: 'sum over dimensions (by / without)' },
	{ name: 'avg', detail: 'average over dimensions' },
	{ name: 'min', detail: 'minimum over dimensions' },
	{ name: 'max', detail: 'maximum over dimensions' },
	{ name: 'count', detail: 'number of samples' }
];

/** Functions of the subset, with their argument shape. */
export const FUNCTIONS: { name: string; detail: string }[] = [
	{ name: 'rate', detail: 'rate(v range-vector) — per-second average increase (counters)' },
	{ name: 'increase', detail: 'increase(v range-vector) — increase over the window (counters)' },
	{ name: 'irate', detail: 'irate(v range-vector) — instant rate from the last two samples' },
	{ name: 'delta', detail: 'delta(v range-vector) — difference first → last (gauges)' },
	{ name: 'sum_over_time', detail: 'sum_over_time(v range-vector)' },
	{ name: 'avg_over_time', detail: 'avg_over_time(v range-vector)' },
	{ name: 'min_over_time', detail: 'min_over_time(v range-vector)' },
	{ name: 'max_over_time', detail: 'max_over_time(v range-vector)' },
	{ name: 'count_over_time', detail: 'count_over_time(v range-vector)' },
	{
		name: 'last_over_time',
		detail: 'last_over_time(v range-vector) — newest sample, keeps __name__'
	},
	{ name: 'abs', detail: 'abs(v instant-vector)' },
	{ name: 'ceil', detail: 'ceil(v instant-vector)' },
	{ name: 'floor', detail: 'floor(v instant-vector)' },
	{ name: 'round', detail: 'round(v instant-vector[, to_nearest=1])' },
	{ name: 'clamp_min', detail: 'clamp_min(v instant-vector, min scalar)' },
	{ name: 'clamp_max', detail: 'clamp_max(v instant-vector, max scalar)' },
	{ name: 'time', detail: 'time() — evaluation timestamp in seconds' },
	{ name: 'vector', detail: 'vector(s scalar) — one sample with no labels' },
	{ name: 'scalar', detail: 'scalar(v instant-vector) — the single sample as a scalar' }
];

export const KEYWORDS: { name: string; detail: string }[] = [
	{ name: 'by', detail: 'aggregate by (labels)' },
	{ name: 'without', detail: 'aggregate without (labels)' },
	{ name: 'bool', detail: 'comparison returns 0/1 instead of filtering' }
];

/** Range durations offered after `[` (daily-gridded counters need ≥ 2d for rate/increase). */
export const RANGES = ['2d', '7d', '30d', '1h', '5m'];

export interface Context {
	kind: 'name' | 'label' | 'value' | 'range';
	prefix: string;
	/** Replacement span [start, end) in the text. */
	start: number;
	end: number;
	/** Metric name in front of the `{` (label/value contexts). */
	metric?: string;
	/** Label name (value context). */
	label?: string;
	/** Whether the opening quote is already typed (value context). */
	quoted?: boolean;
}

const IDENT = /[A-Za-z_:][A-Za-z0-9_:]*$/;

/** Finds what the caret is completing; null when nothing sensible applies. */
export function contextAt(text: string, caret: number): Context | null {
	const before = text.slice(0, caret);
	// inside [ … ] → range durations
	const openBracket = before.lastIndexOf('[');
	if (openBracket >= 0 && before.indexOf(']', openBracket) < 0) {
		const prefix = before.slice(openBracket + 1);
		if (/^[0-9a-z]*$/i.test(prefix))
			return { kind: 'range', prefix, start: openBracket + 1, end: caret };
		return null;
	}
	// inside { … } → label names or values
	const openBrace = lastUnclosedBrace(before);
	if (openBrace >= 0) {
		const inside = before.slice(openBrace + 1);
		const metric = IDENT.exec(before.slice(0, openBrace))?.[0];
		const val = /([A-Za-z_][A-Za-z0-9_]*)\s*(=~|!~|!=|=)\s*("?)([^"]*)$/.exec(inside);
		if (val) {
			const prefix = val[4] ?? '';
			return {
				kind: 'value',
				prefix,
				start: caret - prefix.length,
				end: caret,
				metric,
				label: val[1],
				quoted: val[3] === '"'
			};
		}
		const lab = /(?:^|[{,])\s*([A-Za-z_][A-Za-z0-9_]*)?$/.exec(inside);
		if (lab) {
			const prefix = lab[1] ?? '';
			return { kind: 'label', prefix, start: caret - prefix.length, end: caret, metric };
		}
		return null;
	}
	const m = IDENT.exec(before);
	if (m) return { kind: 'name', prefix: m[0], start: caret - m[0].length, end: caret };
	return null;
}

function lastUnclosedBrace(s: string): number {
	let depth = 0;
	let inStr = false;
	let last = -1;
	for (let i = 0; i < s.length; i++) {
		const c = s[i];
		if (inStr) {
			if (c === '\\') i++;
			else if (c === '"') inStr = false;
			continue;
		}
		if (c === '"') inStr = true;
		else if (c === '{') {
			depth++;
			last = i;
		} else if (c === '}') {
			depth--;
			if (depth <= 0) last = -1;
		}
	}
	return depth > 0 ? last : -1;
}

export interface Sources {
	metrics: readonly string[];
	meta: Record<string, MetricMeta>;
	labels: readonly string[];
	values: readonly string[];
}

function matches(name: string, prefix: string): boolean {
	return prefix === '' || name.toLowerCase().includes(prefix.toLowerCase());
}

function rank(name: string, prefix: string): number {
	const n = name.toLowerCase();
	const p = prefix.toLowerCase();
	if (n === p) return 0;
	if (n.startsWith(p)) return 1;
	return 2;
}

/** Builds the suggestion list for a context (≤ 40 items, prefix matches first). */
export function suggest(ctx: Context, src: Sources): Completion[] {
	const out: Completion[] = [];
	const p = ctx.prefix;
	switch (ctx.kind) {
		case 'name': {
			for (const m of src.metrics)
				if (matches(m, p))
					out.push({ label: m, kind: 'metric', detail: src.meta[m]?.help, insert: m });
			for (const f of FUNCTIONS)
				if (matches(f.name, p))
					out.push({
						label: f.name,
						kind: 'function',
						detail: f.detail,
						insert: `${f.name}(`,
						caretShift: 0
					});
			for (const a of AGGREGATIONS)
				if (matches(a.name, p))
					out.push({ label: a.name, kind: 'aggregation', detail: a.detail, insert: `${a.name}(` });
			for (const k of KEYWORDS)
				if (matches(k.name, p))
					out.push({ label: k.name, kind: 'keyword', detail: k.detail, insert: k.name });
			break;
		}
		case 'label':
			for (const l of src.labels)
				if (l !== '__name__' && matches(l, p))
					out.push({ label: l, kind: 'label', insert: `${l}=""`, caretShift: -1 });
			break;
		case 'value':
			for (const v of src.values)
				if (matches(v, p))
					out.push({ label: v, kind: 'value', insert: (ctx.quoted ? '' : '"') + v + '"' });
			break;
		case 'range':
			for (const r of RANGES)
				if (matches(r, p)) out.push({ label: r, kind: 'keyword', insert: `${r}]` });
			break;
	}
	return out
		.sort((a, b) => rank(a.label, p) - rank(b.label, p) || a.label.localeCompare(b.label))
		.slice(0, 40);
}

/** Applies a completion to the text; returns the new text and caret. */
export function apply(text: string, ctx: Context, c: Completion): { text: string; caret: number } {
	const next = text.slice(0, ctx.start) + c.insert + text.slice(ctx.end);
	const caret = ctx.start + c.insert.length + (c.caretShift ?? 0);
	return { text: next, caret };
}
