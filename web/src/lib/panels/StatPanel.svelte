<script lang="ts">
	// Stat body: the big number is the query result (never typed), formatted
	// in the panel's unit; a sparkline when graph_mode is `area` and a range
	// result exists; colour from thresholds (color_mode value/background).
	import type { Panel, Readyz } from '$lib/api/types.gen';
	import Sparkline from '$lib/charts/Sparkline.svelte';
	import EmptyState from './EmptyState.svelte';
	import {
		colorMode,
		emptyMessage,
		firstError,
		graphMode,
		isEmpty,
		sparkPoints,
		statValues,
		type PanelRun
	} from './model';
	import { formatValue, paletteVar, thresholdColor } from './units';

	let { panel, run, readyz }: { panel: Panel; run: PanelRun | null; readyz: Readyz | null } =
		$props();

	let error = $derived(run ? firstError(run) : undefined);
	let empty = $derived(run ? isEmpty(run) : false);
	let values = $derived(run ? statValues(run) : []);
	let spark = $derived(run && graphMode(panel) === 'area' ? sparkPoints(run) : undefined);
	let mode = $derived(colorMode(panel));
	let firstExpr = $derived(panel.targets.find((t) => !t.hide)?.expr);

	function colorFor(v: number) {
		const c = thresholdColor(v, panel.thresholds, panel.min ?? 0, panel.max ?? 100);
		return paletteVar(c, 'green');
	}
</script>

{#if !run}
	<p class="loading mono" role="status">querying…</p>
{:else if error}
	<EmptyState message={`Query failed: ${error}`} expr={firstExpr} tone="error" />
{:else if empty || values.length === 0}
	<EmptyState message={emptyMessage(panel.source.kind, readyz)} expr={firstExpr} />
{:else}
	<div class="stat" class:multi={values.length > 1} class:bg={mode === 'background'}>
		{#each values as v (v.name)}
			<div
				class="cell"
				style:--c={colorFor(v.value)}
				style:background={mode === 'background'
					? 'color-mix(in srgb, var(--c) 22%, transparent)'
					: undefined}
			>
				{#if values.length > 1}
					<div class="name mono">{v.name}</div>
				{/if}
				<div
					class="value mono"
					style:color={mode === 'value' || mode === 'background' ? 'var(--c)' : 'var(--fg)'}
				>
					{formatValue(v.value, panel.unit, panel.decimals)}
				</div>
				{#if spark && values.length === 1}
					<div class="spark">
						<Sparkline
							points={spark}
							color={thresholdColor(v.value, panel.thresholds) ?? 'green'}
							height={44}
						/>
					</div>
				{/if}
			</div>
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
	.stat {
		display: grid;
		grid-template-columns: 1fr;
		height: 100%;
		padding: 0.25rem 0.5rem;
		gap: 0.25rem;
		container-type: size;
	}
	.stat.multi {
		grid-template-columns: repeat(auto-fit, minmax(7rem, 1fr));
		align-content: start;
		overflow-y: auto;
	}
	.cell {
		position: relative;
		display: flex;
		flex-direction: column;
		justify-content: center;
		min-height: 0;
		border-radius: 4px;
		overflow: hidden;
	}
	.multi .cell {
		padding: 0.25rem 0.4rem;
		background: var(--panel-2);
	}
	.name {
		font-size: 0.68rem;
		color: var(--fg-muted);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.value {
		font-size: clamp(1.4rem, 28cqh, 3rem);
		font-weight: 600;
		line-height: 1.1;
		letter-spacing: -0.01em;
		font-variant-numeric: tabular-nums;
		text-align: center;
		z-index: 1;
	}
	.multi .value {
		font-size: 1.25rem;
		text-align: left;
	}
	.spark {
		position: absolute;
		left: 0;
		right: 0;
		bottom: 0;
	}
</style>
