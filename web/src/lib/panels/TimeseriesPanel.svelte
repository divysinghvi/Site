<script lang="ts">
	// Timeseries body: every visible range target's matrix goes into one uPlot
	// (colours continue across targets), stacking per panel.stack, thresholds
	// as dashed lines. Empty → the honest empty state.
	import type { PaletteColor, Panel, Readyz } from '$lib/api/types.gen';
	import UPlotChart from '$lib/charts/UPlotChart.svelte';
	import { matrixToChart, type ChartData } from '$lib/charts/uplot';
	import EmptyState from './EmptyState.svelte';
	import { emptyMessage, firstError, isEmpty, visibleResults, type PanelRun } from './model';

	let {
		panel,
		run,
		readyz,
		syncKey = 'divy-dashboard',
		legendVisible = $bindable(true)
	}: {
		panel: Panel;
		run: PanelRun | null;
		readyz: Readyz | null;
		syncKey?: string;
		legendVisible?: boolean;
	} = $props();

	let error = $derived(run ? firstError(run) : undefined);
	let empty = $derived(run ? isEmpty(run) : false);
	let firstExpr = $derived(panel.targets.find((t) => !t.hide)?.expr);

	let chart = $derived.by((): ChartData => {
		if (!run) return { xs: [], series: [] };
		let offset = 0;
		const parts: ChartData[] = [];
		for (const r of visibleResults(run)) {
			if (r.data?.resultType !== 'matrix') continue;
			const c = matrixToChart(r.data.result, r.target.legendFormat, offset);
			offset += c.series.length;
			parts.push(c);
		}
		if (parts.length === 1) return parts[0]!;
		// merge on the union of timestamps
		const xset = new Set<number>();
		for (const p of parts) for (const x of p.xs) xset.add(x);
		const xs = [...xset].sort((a, b) => a - b);
		const idx = new Map(xs.map((x, i) => [x, i] as const));
		const series = parts.flatMap((p) =>
			p.series.map((s) => {
				const values = new Array<number | null>(xs.length).fill(null);
				p.xs.forEach((x, i) => (values[idx.get(x)!] = s.values[i] ?? null));
				return { ...s, values };
			})
		);
		return { xs, series };
	});

	let thresholds = $derived.by(() => {
		const out: { value: number; color: PaletteColor }[] = [];
		for (const s of panel.thresholds?.steps ?? []) {
			if (s.value !== null) out.push({ value: s.value, color: s.color });
		}
		return out;
	});
</script>

{#if !run}
	<p class="loading mono" role="status">querying…</p>
{:else if error}
	<EmptyState message={`Query failed: ${error}`} expr={firstExpr} tone="error" />
{:else if empty || chart.series.length === 0}
	<EmptyState message={emptyMessage(panel.source.kind, readyz)} expr={firstExpr} />
{:else}
	<UPlotChart
		data={chart}
		unit={panel.unit}
		decimals={panel.decimals}
		stacked={!!panel.stack}
		{syncKey}
		{thresholds}
		xRange={[run.range.from, run.range.to]}
		bind:legendVisible
		label="{panel.title}: {chart.series.length} series over the selected range"
	/>
{/if}

<style>
	.loading {
		margin: 0;
		padding: 1rem;
		font-size: 0.75rem;
		color: var(--fg-dim);
	}
</style>
