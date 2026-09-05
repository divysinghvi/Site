// Annotation templating (content §C.7): only `{{ $value }}` and
// `{{ $labels.<x> }}` are substituted — the two forms content/alerts.yaml is
// validated against. Anything else is left as written.

/** Prometheus-like float rendering: integers plain, others shortest round-trip up to 6 significant digits. */
export function formatValue(v: number | undefined): string {
	if (v === undefined || Number.isNaN(v)) return 'NaN';
	if (!Number.isFinite(v)) return v > 0 ? '+Inf' : '-Inf';
	if (Number.isInteger(v)) return String(v);
	return String(Number(v.toPrecision(6)));
}

export function renderTemplate(
	text: string,
	ctx: { value?: number; labels: Record<string, string | undefined> }
): string {
	return text
		.replace(/\{\{\s*\$value\s*\}\}/g, formatValue(ctx.value))
		.replace(
			/\{\{\s*\$labels\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}/g,
			(_, k: string) => ctx.labels[k] ?? ''
		);
}
