<script lang="ts">
	// Gauge body: a half-circle arc from min to max with threshold markers on
	// the rim; the needle-less fill is coloured by the threshold the value sits
	// in. One gauge per series of the visible target.
	import type { Panel, Readyz } from '$lib/api/types.gen';
	import EmptyState from './EmptyState.svelte';
	import {
		emptyMessage,
		firstError,
		isEmpty,
		showThresholdMarkers,
		statValues,
		type PanelRun
	} from './model';
	import { formatValue, paletteVar, thresholdColor } from './units';

	let { panel, run, readyz }: { panel: Panel; run: PanelRun | null; readyz: Readyz | null } =
		$props();

	let error = $derived(run ? firstError(run) : undefined);
	let empty = $derived(run ? isEmpty(run) : false);
	let values = $derived(run ? statValues(run) : []);
	let min = $derived(panel.min ?? 0);
	let max = $derived(panel.max ?? Math.max(1, ...values.map((v) => v.value)));
	let markers = $derived(
		showThresholdMarkers(panel)
			? (panel.thresholds?.steps ?? []).filter(
					(s) => s.value !== null && s.value > min && s.value < max
				)
			: []
	);
	let firstExpr = $derived(panel.targets.find((t) => !t.hide)?.expr);

	// arc geometry (viewBox 0 0 100 60): centre (50,52), radius 40, 180° sweep
	const CX = 50;
	const CY = 52;
	const R = 40;
	function polar(frac: number): [number, number] {
		const a = Math.PI - Math.PI * Math.min(1, Math.max(0, frac));
		return [CX + R * Math.cos(a), CY - R * Math.sin(a)];
	}
	function arc(frac: number): string {
		const f = Math.min(1, Math.max(0, frac));
		const [x, y] = polar(f);
		const [x0, y0] = polar(0);
		const large = f > 0.5 ? 1 : 0;
		return `M${x0.toFixed(2)},${y0.toFixed(2)} A${R},${R} 0 ${large} 1 ${x.toFixed(2)},${y.toFixed(2)}`;
	}
	function frac(v: number): number {
		return max === min ? 0 : (v - min) / (max - min);
	}
</script>

{#if !run}
	<p class="loading mono" role="status">querying…</p>
{:else if error}
	<EmptyState message={`Query failed: ${error}`} expr={firstExpr} tone="error" />
{:else if empty || values.length === 0}
	<EmptyState message={emptyMessage(panel.source.kind, readyz)} expr={firstExpr} />
{:else}
	<div class="gauges" class:multi={values.length > 1}>
		{#each values as v (v.name)}
			{@const color = paletteVar(thresholdColor(v.value, panel.thresholds, min, max), 'green')}
			<figure class="gauge">
				<svg
					viewBox="0 0 100 62"
					role="img"
					aria-label="{v.name}: {formatValue(v.value, panel.unit, panel.decimals)} of {formatValue(
						max,
						panel.unit
					)}"
				>
					<path class="track" d={arc(1)} />
					<path
						class="fill bar-grow-arc"
						d={arc(frac(v.value))}
						style:stroke={color}
						pathLength="1"
					/>
					{#each markers as m (m.value)}
						{@const [mx, my] = polar(frac(m.value ?? 0))}
						<circle cx={mx} cy={my} r="2.4" style:fill="var(--{m.color})" class="marker" />
					{/each}
					<text x={CX} y={CY - 6} class="value" text-anchor="middle" style:fill={color}>
						{formatValue(v.value, panel.unit, panel.decimals)}
					</text>
					<text x={polar(0)[0]} y={CY + 8} class="bound" text-anchor="middle"
						>{formatValue(min, panel.unit)}</text
					>
					<text x={polar(1)[0]} y={CY + 8} class="bound" text-anchor="middle"
						>{formatValue(max, panel.unit)}</text
					>
				</svg>
				{#if values.length > 1}
					<figcaption class="mono">{v.name}</figcaption>
				{/if}
			</figure>
		{/each}
	</div>
{/if}

<style>
	.loading {
		margin: 0;
		padding: 1rem;
		font-size: 0.75rem;
		color: var(--fg-dim);
	}
	.gauges {
		display: grid;
		grid-template-columns: 1fr;
		height: 100%;
		padding: 0.25rem 0.5rem;
		gap: 0.25rem;
	}
	.gauges.multi {
		grid-template-columns: repeat(auto-fit, minmax(8rem, 1fr));
		overflow-y: auto;
	}
	.gauge {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		margin: 0;
		min-height: 0;
	}
	svg {
		width: 100%;
		height: 100%;
		max-height: 100%;
	}
	.track {
		fill: none;
		stroke: var(--panel-3);
		stroke-width: 9;
		stroke-linecap: butt;
	}
	.fill {
		fill: none;
		stroke-width: 9;
		stroke-linecap: butt;
	}
	@media (prefers-reduced-motion: no-preference) {
		.fill {
			stroke-dasharray: 1;
			stroke-dashoffset: 1;
			animation: gauge-draw 700ms cubic-bezier(0.2, 0.7, 0.2, 1) forwards;
		}
	}
	@keyframes gauge-draw {
		to {
			stroke-dashoffset: 0;
		}
	}
	.marker {
		stroke: var(--panel);
		stroke-width: 1;
	}
	.value {
		font-family: var(--font-mono);
		font-size: 15px;
		font-weight: 600;
	}
	.bound {
		font-family: var(--font-mono);
		font-size: 6px;
		fill: var(--fg-dim);
	}
	figcaption {
		font-size: 0.68rem;
		color: var(--fg-muted);
	}
</style>
