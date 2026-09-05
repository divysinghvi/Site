<script lang="ts">
	// A uPlot timeseries: crosshair (synced through `syncKey`), an HTML tooltip
	// on the hovered chart, a legend with per-series toggles (re-stacking when
	// `stacked`), threshold lines, resize via ResizeObserver, and a draw-in
	// animation (CSS clip on the canvas, off under prefers-reduced-motion).
	// uPlot is imported lazily on mount so nothing runs during prerender.
	import { onMount, untrack } from 'svelte';
	import type uPlot from 'uplot';
	import 'uplot/dist/uPlot.min.css';
	import type { PaletteColor } from '$lib/api/types.gen';
	import { theme } from '$lib/state/theme.svelte';
	import { motion } from '$lib/state/motion.svelte';
	import { formatValue, type Unit } from '$lib/panels/units';
	import {
		buildOptions,
		formatX,
		readTheme,
		stack,
		toAligned,
		type ChartData,
		type ThemeColors
	} from './uplot';

	let {
		data,
		unit = undefined,
		decimals = undefined,
		stacked = false,
		syncKey = undefined,
		thresholds = [],
		legendVisible = $bindable(true),
		label = 'Timeseries chart',
		xRange = undefined
	}: {
		data: ChartData;
		unit?: Unit;
		decimals?: number;
		stacked?: boolean;
		syncKey?: string;
		thresholds?: { value: number; color: PaletteColor }[];
		legendVisible?: boolean;
		label?: string;
		/** The queried [from, to] in unix seconds; keeps sparse results on the real axis. */
		xRange?: [number, number];
	} = $props();

	let host = $state<HTMLDivElement | null>(null);
	let plot: uPlot | null = null;
	let UPlotCtor: typeof uPlot | null = null;
	let size = $state({ w: 0, h: 0 });
	let hovering = $state(false);
	let cursorIdx = $state<number | null>(null);
	let tipLeft = $state(0);
	let hidden = $state<Set<string>>(new Set());
	let themeColors = $state<ThemeColors | null>(null);
	let drawn = $state(false);
	let loadError = $state<string | null>(null);
	let signature = '';

	let shown = $derived(data.series.map((s) => !hidden.has(s.name)));
	let stackedView = $derived(stacked ? stack(data, shown) : null);
	let spanS = $derived(data.xs.length > 1 ? data.xs[data.xs.length - 1]! - data.xs[0]! : 0);
	let fewPoints = $derived(data.xs.length > 0 && data.xs.length <= 40);
	let colorOf = (c: PaletteColor) => themeColors?.palette[c] ?? `var(--${c})`;

	function signatureOf(): string {
		return [
			data.series.map((s) => s.name).join('|'),
			stacked,
			theme.current,
			unit,
			decimals,
			thresholds.map((t) => `${t.value}:${t.color}`).join(','),
			xRange ? `${xRange[0]}-${xRange[1]}` : ''
		].join('#');
	}

	function build() {
		if (!host || !UPlotCtor || size.w < 10 || size.h < 10) return;
		plot?.destroy();
		plot = null;
		themeColors = readTheme();
		const view = stackedView ? stackedView.data : data;
		const opts = buildOptions({
			width: size.w,
			height: size.h,
			unit,
			decimals,
			stacked,
			syncKey,
			theme: themeColors,
			series: data.series,
			shown,
			bands: stackedView?.bands,
			thresholds: thresholds.map((t) => ({ value: t.value, color: themeColors!.palette[t.color] })),
			points: fewPoints,
			xRange,
			onCursor: (idx) => {
				cursorIdx = idx;
				if (idx != null && plot) tipLeft = plot.valToPos(data.xs[idx]!, 'x');
			}
		});
		plot = new UPlotCtor(opts, toAligned(view), host);
		signature = signatureOf();
		drawn = true;
	}

	onMount(() => {
		let cancelled = false;
		import('uplot')
			.then((m) => {
				if (cancelled) return;
				UPlotCtor = m.default;
				build();
			})
			.catch((e: unknown) => {
				loadError = e instanceof Error ? e.message : String(e);
			});
		const ro = new ResizeObserver((entries) => {
			const r = entries[0]?.contentRect;
			if (!r) return;
			size = { w: Math.floor(r.width), h: Math.floor(r.height) };
		});
		if (host) ro.observe(host);
		return () => {
			cancelled = true;
			ro.disconnect();
			plot?.destroy();
			plot = null;
		};
	});

	// resize → setSize; data/series/theme changes → rebuild or setData
	$effect(() => {
		const { w, h } = size;
		if (!plot) {
			untrack(build);
			return;
		}
		if (w >= 10 && h >= 10) plot.setSize({ width: w, height: h });
	});

	$effect(() => {
		// dependencies: data, shown, stacked, theme.current, thresholds
		void data;
		void shown;
		void theme.current;
		void thresholds;
		void xRange;
		const view = stackedView ? stackedView.data : data;
		if (!plot || !UPlotCtor) return;
		if (signatureOf() !== signature) {
			untrack(build);
			return;
		}
		plot.setData(toAligned(view));
		if (stackedView) {
			plot.delBand(null);
			for (const b of stackedView.bands) plot.addBand(b);
		}
		data.series.forEach((_, i) => plot!.setSeries(i + 1, { show: shown[i] ?? true }, false));
		plot.redraw(false);
	});

	function toggle(name: string) {
		const next = new Set(hidden);
		if (next.has(name)) next.delete(name);
		else next.add(name);
		hidden = next;
	}

	function soloOrAll(name: string) {
		// double-click: show only this series; again → all
		const onlyThis = data.series.every((s) => s.name === name || hidden.has(s.name));
		hidden = onlyThis
			? new Set()
			: new Set(data.series.filter((s) => s.name !== name).map((s) => s.name));
	}

	let tipRows = $derived.by(() => {
		if (cursorIdx == null) return [];
		const i = cursorIdx;
		return data.series
			.map((s, k) => ({ s, k, v: s.values[i] ?? null }))
			.filter(({ k }) => shown[k])
			.map(({ s, v }) => ({
				name: s.name,
				color: s.color,
				text: v == null ? '–' : formatValue(v, unit, decimals)
			}));
	});
	let tipTime = $derived(cursorIdx == null ? '' : formatX(data.xs[cursorIdx] ?? 0, spanS));
	let tipRight = $derived(tipLeft > size.w * 0.6);
</script>

<div class="chart-wrap" class:draw-in={drawn && !motion.reduced}>
	<div
		class="chart"
		bind:this={host}
		role="img"
		aria-label={label}
		onpointerenter={() => (hovering = true)}
		onpointerleave={() => (hovering = false)}
	>
		{#if loadError}
			<p class="load-error mono" role="status">chart library failed to load: {loadError}</p>
		{/if}
		{#if hovering && cursorIdx != null && tipRows.length}
			<div
				class="tip mono"
				class:right={tipRight}
				style:left={tipRight ? 'auto' : `${tipLeft + 14}px`}
				style:right={tipRight ? `${size.w - tipLeft + 14}px` : 'auto'}
				aria-hidden="true"
			>
				<div class="tip-time">{tipTime}</div>
				{#each tipRows as r (r.name)}
					<div class="tip-row">
						<span class="swatch" style:background={colorOf(r.color)}></span>
						<span class="tip-name">{r.name}</span>
						<span class="tip-val">{r.text}</span>
					</div>
				{/each}
			</div>
		{/if}
	</div>
	{#if legendVisible && data.series.length > 0}
		<ul class="legend" aria-label="Series legend">
			{#each data.series as s, i (s.name)}
				<li>
					<button
						type="button"
						class="legend-item"
						class:off={!shown[i]}
						aria-pressed={shown[i]}
						title="Click to toggle, double-click to isolate"
						onclick={() => toggle(s.name)}
						ondblclick={() => soloOrAll(s.name)}
					>
						<span class="swatch" style:background={colorOf(s.color)}></span>
						<span class="legend-name">{s.name}</span>
					</button>
				</li>
			{/each}
		</ul>
	{/if}
</div>

<style>
	.chart-wrap {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
	}
	.chart {
		position: relative;
		flex: 1;
		min-height: 0;
		overflow: hidden;
	}
	.chart :global(.uplot) {
		font-family: var(--font-mono);
	}
	.load-error {
		margin: 0;
		padding: 1rem;
		font-size: 0.75rem;
		color: var(--red);
	}
	.chart :global(.u-over) {
		cursor: crosshair;
	}
	.chart :global(.u-cursor-x) {
		border-right: 1px dashed color-mix(in srgb, var(--fg) 45%, transparent);
	}
	.chart :global(.u-cursor-pt) {
		border-width: 2px;
	}
	@media (prefers-reduced-motion: no-preference) {
		.draw-in :global(canvas) {
			animation: chart-draw 700ms cubic-bezier(0.2, 0.7, 0.2, 1) both;
		}
	}
	@keyframes chart-draw {
		from {
			clip-path: inset(0 100% 0 0);
		}
		to {
			clip-path: inset(0 0 0 0);
		}
	}
	.tip {
		position: absolute;
		top: 8px;
		z-index: 5;
		max-width: min(320px, 70%);
		padding: 0.35rem 0.5rem;
		border: 1px solid var(--border);
		border-radius: 4px;
		background: color-mix(in srgb, var(--panel-2) 94%, transparent);
		box-shadow: 0 4px 16px rgba(0, 0, 0, 0.35);
		font-size: 0.72rem;
		pointer-events: none;
	}
	.tip-time {
		margin-bottom: 0.2rem;
		color: var(--fg-muted);
	}
	.tip-row {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		white-space: nowrap;
	}
	.tip-name {
		overflow: hidden;
		text-overflow: ellipsis;
		color: var(--fg-muted);
	}
	.tip-val {
		margin-left: auto;
		font-variant-numeric: tabular-nums;
		color: var(--fg);
	}
	.swatch {
		display: inline-block;
		width: 10px;
		height: 4px;
		border-radius: 2px;
		flex: none;
	}
	.legend {
		display: flex;
		flex-wrap: wrap;
		gap: 0 0.25rem;
		margin: 0;
		padding: 0.15rem 0.5rem 0.25rem;
		list-style: none;
		max-height: 3.4rem;
		overflow-y: auto;
	}
	.legend-item {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		min-height: 1.5rem;
		padding: 0 0.35rem;
		border: 0;
		border-radius: 3px;
		background: transparent;
		color: var(--fg-muted);
		font-size: 0.72rem;
		font-family: var(--font-mono);
		cursor: pointer;
	}
	.legend-item:hover {
		background: var(--panel-2);
		color: var(--fg);
	}
	.legend-item.off {
		color: var(--fg-dim);
		text-decoration: line-through;
	}
	.legend-item.off .swatch {
		opacity: 0.35;
	}
	.legend-name {
		max-width: 14rem;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
</style>
