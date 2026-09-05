<script lang="ts">
	// Bar gauge body: one horizontal bar per series of an instant vector,
	// sorted by value, labelled with legendFormat, coloured by thresholds.
	import type { Panel, Readyz } from '$lib/api/types.gen';
	import EmptyState from './EmptyState.svelte';
	import { emptyMessage, firstError, isEmpty, statValues, type PanelRun } from './model';
	import { formatValue, paletteVar, thresholdColor } from './units';

	let { panel, run, readyz }: { panel: Panel; run: PanelRun | null; readyz: Readyz | null } =
		$props();

	let error = $derived(run ? firstError(run) : undefined);
	let empty = $derived(run ? isEmpty(run) : false);
	let rows = $derived(run ? [...statValues(run)].sort((a, b) => b.value - a.value) : []);
	let min = $derived(panel.min ?? 0);
	let max = $derived(panel.max ?? Math.max(1, ...rows.map((r) => r.value)));
	let firstExpr = $derived(panel.targets.find((t) => !t.hide)?.expr);

	function pct(v: number): number {
		if (max === min) return 0;
		return Math.min(100, Math.max(0, ((v - min) / (max - min)) * 100));
	}
</script>

{#if !run}
	<p class="loading mono" role="status">querying…</p>
{:else if error}
	<EmptyState message={`Query failed: ${error}`} expr={firstExpr} tone="error" />
{:else if empty || rows.length === 0}
	<EmptyState message={emptyMessage(panel.source.kind, readyz)} expr={firstExpr} />
{:else}
	<ul class="bars" aria-label="{panel.title} by series">
		{#each rows as r (r.name)}
			<li class="row">
				<span class="name mono" title={r.name}>{r.name}</span>
				<span class="track" aria-hidden="true">
					<span
						class="bar bar-grow"
						style:width="{pct(r.value)}%"
						style:background={paletteVar(
							thresholdColor(r.value, panel.thresholds, min, max),
							'green'
						)}
					></span>
				</span>
				<span class="val mono">{formatValue(r.value, panel.unit, panel.decimals)}</span>
			</li>
		{/each}
	</ul>
{/if}

<style>
	.loading {
		margin: 0;
		padding: 1rem;
		font-size: 0.75rem;
		color: var(--fg-dim);
	}
	.bars {
		display: flex;
		flex-direction: column;
		gap: 0.35rem;
		height: 100%;
		margin: 0;
		padding: 0.5rem 0.75rem;
		list-style: none;
		overflow-y: auto;
	}
	.row {
		display: grid;
		grid-template-columns: minmax(5rem, 30%) 1fr auto;
		align-items: center;
		gap: 0.5rem;
		font-size: 0.75rem;
	}
	.name {
		color: var(--fg-muted);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.track {
		display: block;
		height: 1.1rem;
		border-radius: 3px;
		background: var(--panel-2);
		overflow: hidden;
	}
	.bar {
		display: block;
		height: 100%;
		border-radius: 3px;
		min-width: 2px;
	}
	.val {
		min-width: 2.5rem;
		text-align: right;
		font-variant-numeric: tabular-nums;
	}
</style>
