// Stream-selector helpers for the level chips (review coverage-12): read the
// `level` matcher of the first selector and rewrite it as a multi-select —
// one level → level="warn", several → level=~"warn|error", every level (or
// none) → the matcher is removed. Only the first `{…}` is touched; the rest of
// the query (line filters, | json, label filters, metric wrappers) is kept
// byte for byte.

/** The four levels of content/logs.ndjson in display order. */
export const LEVELS = ['error', 'warn', 'info', 'debug'] as const;
export type Level = (typeof LEVELS)[number];

/** Default query when everything else would leave an empty selector. */
export const DEFAULT_SELECTOR = '{service=~".+"}';

export interface Matcher {
	name: string;
	op: '=' | '!=' | '=~' | '!~';
	/** Unquoted value. */
	value: string;
	/** Byte span of the whole matcher inside the query. */
	start: number;
	end: number;
}

export interface Selector {
	/** Index of `{` and of `}` in the query. */
	open: number;
	close: number;
	matchers: Matcher[];
}

/** Unquotes a `"…"` (Go escapes) or `` `…` `` string literal. */
function unquote(lit: string): string {
	if (lit.startsWith('`')) return lit.slice(1, -1);
	try {
		return JSON.parse(lit) as string;
	} catch {
		return lit.slice(1, -1);
	}
}

function quote(v: string): string {
	return JSON.stringify(v);
}

/** Finds the first `{…}` selector and its matchers; null when there is none. */
export function firstSelector(query: string): Selector | null {
	const open = query.indexOf('{');
	if (open < 0) return null;
	// the closing brace: skip string literals
	let i = open + 1;
	let close = -1;
	while (i < query.length) {
		const c = query[i];
		if (c === '"') {
			i++;
			while (i < query.length && query[i] !== '"') {
				if (query[i] === '\\') i++;
				i++;
			}
		} else if (c === '`') {
			i++;
			while (i < query.length && query[i] !== '`') i++;
		} else if (c === '}') {
			close = i;
			break;
		}
		i++;
	}
	if (close < 0) return null;
	const inside = query.slice(open + 1, close);
	const re = /([A-Za-z_][A-Za-z0-9_]*)\s*(=~|!~|!=|=)\s*("(?:[^"\\]|\\.)*"|`[^`]*`)/g;
	const matchers: Matcher[] = [];
	let m: RegExpExecArray | null;
	while ((m = re.exec(inside))) {
		matchers.push({
			name: m[1]!,
			op: m[2] as Matcher['op'],
			value: unquote(m[3]!),
			start: open + 1 + m.index,
			end: open + 1 + m.index + m[0].length
		});
	}
	return { open, close, matchers };
}

/**
 * The levels the query's first selector keeps: `all` when there is no level
 * matcher (or one the chips cannot express, e.g. `level!="debug"`), else the
 * list of levels it selects.
 */
export function selectedLevels(query: string): Level[] | 'all' {
	const sel = firstSelector(query);
	const m = sel?.matchers.find((x) => x.name === 'level');
	if (!m) return 'all';
	const levels = (v: string) =>
		v
			.split('|')
			.map((s) => s.trim())
			.filter((s): s is Level => (LEVELS as readonly string[]).includes(s));
	if (m.op === '=') return levels(m.value);
	if (m.op === '=~') {
		const inner = m.value.replace(/^\(|\)$/g, '');
		const ls = levels(inner);
		// only plain alternations of known levels are ours
		if (ls.length && ls.join('|') === inner) return ls;
	}
	return 'all';
}

/**
 * Rewrites the `level` matcher of the first selector. Every level (or none)
 * removes the matcher; a query without a selector gets one. Returns the query
 * unchanged when the matcher already says the same thing.
 */
export function withLevels(query: string, levels: readonly Level[]): string {
	const ordered = LEVELS.filter((l) => levels.includes(l));
	const all = ordered.length === 0 || ordered.length === LEVELS.length;
	const matcherText = all
		? ''
		: ordered.length === 1
			? `level=${quote(ordered[0]!)}`
			: `level=~${quote(ordered.join('|'))}`;
	const sel = firstSelector(query);
	if (!sel) {
		const q = query.trim();
		if (all) return q || DEFAULT_SELECTOR;
		return `{${matcherText}}` + (q ? ' ' + q : '');
	}
	const existing = sel.matchers.find((x) => x.name === 'level');
	if (existing) {
		const others = sel.matchers.filter((x) => x !== existing);
		if (all) {
			if (others.length === 0) {
				// {level="x"} → the default selector, never {}
				return query.slice(0, sel.open) + DEFAULT_SELECTOR + query.slice(sel.close + 1);
			}
			// remove the matcher and the comma next to it
			let s = existing.start;
			let e = existing.end;
			const before = query.slice(sel.open + 1, s);
			const after = query.slice(e, sel.close);
			const trailing = /^\s*,\s*/.exec(after);
			const leading = /\s*,\s*$/.exec(before);
			if (trailing) e += trailing[0].length;
			else if (leading) s -= leading[0].length;
			return query.slice(0, s) + query.slice(e);
		}
		return query.slice(0, existing.start) + matcherText + query.slice(existing.end);
	}
	if (all) return query;
	const inside = query.slice(sel.open + 1, sel.close).trim();
	const joined = inside ? `${inside}, ${matcherText}` : matcherText;
	return query.slice(0, sel.open) + '{' + joined + '}' + query.slice(sel.close + 1);
}
