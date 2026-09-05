// LogQL autocomplete (review coverage-12): label names from
// /loki/api/v1/labels, values from /label/{name}/values when the caret is inside
// {…}, the pipeline stages (| json, |=, !=, |~, !~, label filters) and the
// subset's functions/aggregations from a static list mirroring
// docs/logql-subset.md §1. Same Context/Completion/apply shape as
// $lib/panels/promqlComplete so the query bar component is a sibling.

export type CompletionKind =
	| 'label'
	| 'value'
	| 'stage'
	| 'parser'
	| 'function'
	| 'aggregation'
	| 'keyword'
	| 'field'
	| 'selector';

export interface Completion {
	label: string;
	kind: CompletionKind;
	detail?: string;
	insert: string;
	/** Caret offset from the end of `insert` (negative = move back). */
	caretShift?: number;
}

/** Stages of the subset (docs/logql-subset.md §1: line filters, parser, label filter). */
export const STAGES: { name: string; detail: string; insert: string; caretShift: number }[] = [
	{ name: '|= "…"', detail: 'line contains (case-sensitive)', insert: '|= ""', caretShift: -1 },
	{ name: '!= "…"', detail: 'line does not contain', insert: '!= ""', caretShift: -1 },
	{ name: '|~ "…"', detail: 'line matches RE2 (unanchored; (?i) for case-insensitive)', insert: '|~ ""', caretShift: -1 },
	{ name: '!~ "…"', detail: 'line does not match RE2', insert: '!~ ""', caretShift: -1 },
	{ name: '| json', detail: 'extract the line\'s JSON fields as labels', insert: '| json', caretShift: 0 }
];

/** Range functions of the subset. */
export const FUNCTIONS: { name: string; detail: string }[] = [
	{ name: 'count_over_time', detail: 'count_over_time({…} …[range]) — entries per step' },
	{ name: 'rate', detail: 'rate({…} …[range]) — entries per second' }
];

export const AGGREGATIONS: { name: string; detail: string }[] = [
	{ name: 'sum', detail: 'sum [by|without (…)] (range function)' },
	{ name: 'count', detail: 'count of series' },
	{ name: 'min', detail: 'minimum' },
	{ name: 'max', detail: 'maximum' },
	{ name: 'avg', detail: 'average' }
];

export const KEYWORDS: { name: string; detail: string }[] = [
	{ name: 'by', detail: 'by (labels)' },
	{ name: 'without', detail: 'without (labels)' },
	{ name: 'and', detail: 'label filter conjunction' },
	{ name: 'or', detail: 'label filter disjunction' },
	{ name: 'vector', detail: 'vector(N) — scalar literal (Grafana\'s health check)' }
];

/** Filter operators offered after a label name in a `| …` stage. */
export const FILTER_OPS = ['=', '!=', '=~', '!~', '==', '>', '>=', '<', '<='] as const;

/** Range durations offered after `[`. */
export const RANGES = ['1h', '1d', '7d', '30d', '90d', '1y'];

export interface Context {
	kind: 'name' | 'label' | 'value' | 'range' | 'stage' | 'pipe';
	prefix: string;
	start: number;
	end: number;
	/** Label name (value context). */
	label?: string;
	/** Whether the opening quote is already typed (value context). */
	quoted?: boolean;
}

const IDENT = /[A-Za-z_][A-Za-z0-9_]*$/;

function lastUnclosedBrace(s: string): number {
	let depth = 0;
	let inStr = false;
	let quoteCh = '';
	let last = -1;
	for (let i = 0; i < s.length; i++) {
		const c = s[i]!;
		if (inStr) {
			if (c === '\\' && quoteCh === '"') i++;
			else if (c === quoteCh) inStr = false;
			continue;
		}
		if (c === '"' || c === '`') {
			inStr = true;
			quoteCh = c;
		} else if (c === '{') {
			depth++;
			last = i;
		} else if (c === '}') {
			depth--;
			if (depth <= 0) last = -1;
		}
	}
	return depth > 0 ? last : -1;
}

function insideString(s: string): boolean {
	let inStr = false;
	let quoteCh = '';
	for (let i = 0; i < s.length; i++) {
		const c = s[i]!;
		if (inStr) {
			if (c === '\\' && quoteCh === '"') i++;
			else if (c === quoteCh) inStr = false;
			continue;
		}
		if (c === '"' || c === '`') {
			inStr = true;
			quoteCh = c;
		}
	}
	return inStr;
}

/**
 * What the caret is completing; null when nothing applies. `force` (Ctrl+Space)
 * also returns empty-prefix contexts, which the bar hides otherwise.
 */
export function contextAt(text: string, caret: number, force = false): Context | null {
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
		const val = /([A-Za-z_][A-Za-z0-9_]*)\s*(=~|!~|!=|=)\s*("?)([^"]*)$/.exec(inside);
		if (val) {
			const prefix = val[4] ?? '';
			return {
				kind: 'value',
				prefix,
				start: caret - prefix.length,
				end: caret,
				label: val[1],
				quoted: val[3] === '"'
			};
		}
		const lab = /(?:^|[{,])\s*([A-Za-z_][A-Za-z0-9_]*)?$/.exec(inside);
		if (lab) {
			const prefix = lab[1] ?? '';
			return { kind: 'label', prefix, start: caret - prefix.length, end: caret };
		}
		return null;
	}
	if (insideString(before)) return null;
	// after a pipe: `| json`, `| <label> = "…"`
	const pipe = /\|\s*([A-Za-z_][A-Za-z0-9_]*)?$/.exec(before);
	if (pipe) {
		const prefix = pipe[1] ?? '';
		return { kind: 'stage', prefix, start: caret - prefix.length, end: caret };
	}
	// right after a selector, a string or a closing bracket + whitespace → stage operators
	if (/[}"`)\]]\s+$/.test(before) || (force && /[}"`)\]]\s*$/.test(before))) {
		return { kind: 'pipe', prefix: '', start: caret, end: caret };
	}
	const m = IDENT.exec(before);
	if (m) return { kind: 'name', prefix: m[0], start: caret - m[0].length, end: caret };
	if (force && /^\s*$|[(\s]$/.test(before))
		return { kind: 'name', prefix: '', start: caret, end: caret };
	return null;
}

export interface Sources {
	/** Stream label names (/loki/api/v1/labels). */
	labels: readonly string[];
	/** Values of the label in a value context. */
	values: readonly string[];
	/** Field names of the lines (what `| json` extracts), from the current result. */
	fields: readonly string[];
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
			if (p === '' || '{'.startsWith(p))
				out.push({
					label: '{…}',
					kind: 'selector',
					detail: 'stream selector, e.g. {service="gradr"}',
					insert: '{}',
					caretShift: -1
				});
			for (const f of FUNCTIONS)
				if (matches(f.name, p))
					out.push({ label: f.name, kind: 'function', detail: f.detail, insert: `${f.name}(` });
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
				if (matches(l, p)) out.push({ label: l, kind: 'label', insert: `${l}=""`, caretShift: -1 });
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
		case 'stage': {
			if (matches('json', p))
				out.push({ label: 'json', kind: 'parser', detail: STAGES[4]!.detail, insert: 'json' });
			const names = new Set<string>([...src.labels, ...src.fields]);
			for (const n of [...names].sort())
				if (matches(n, p))
					out.push({
						label: n,
						kind: src.fields.includes(n) && !src.labels.includes(n) ? 'field' : 'label',
						detail: 'label filter: = != =~ !~ (strings), == > >= < <= (numbers)',
						insert: `${n} = ""`,
						caretShift: -1
					});
			break;
		}
		case 'pipe':
			for (const s of STAGES)
				out.push({
					label: s.name,
					kind: s.insert.includes('json') ? 'parser' : 'stage',
					detail: s.detail,
					insert: s.insert,
					caretShift: s.caretShift
				});
			out.push({
				label: '| <label> = "…"',
				kind: 'stage',
				detail: 'label filter (after | json for line fields)',
				insert: '| ',
				caretShift: 0
			});
			return out;
	}
	return out
		.sort((a, b) => rank(a.label, p) - rank(b.label, p) || a.label.localeCompare(b.label))
		.slice(0, 40);
}

/** Applies a completion; returns the new text and caret. */
export function apply(text: string, ctx: Context, c: Completion): { text: string; caret: number } {
	const next = text.slice(0, ctx.start) + c.insert + text.slice(ctx.end);
	const caret = ctx.start + c.insert.length + (c.caretShift ?? 0);
	return { text: next, caret };
}
