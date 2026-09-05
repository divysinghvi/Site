<script lang="ts">
	// Desktop waterfall: tick header + one row per visible span (DFS order).
	// Rows are an ARIA tree with a roving tabindex; ↑/↓/Home/End move focus
	// here, Enter/Space/←/→ are handled by the row, j/k globally by the viewer.
	import type { TraceModel, TraceNode } from '$lib/trace/model';
	import { ticks, pct, clamp } from '$lib/trace/axis';
	import SpanRow from './SpanRow.svelte';

	let {
		model,
		visible,
		v0,
		v1,
		nowUs,
		focusedId,
		selectedId,
		collapsed,
		animate,
		onmove,
		onfocus,
		onselect,
		ontoggle
	}: {
		model: TraceModel;
		visible: TraceNode[];
		v0: number;
		v1: number;
		nowUs: number | undefined;
		focusedId: string | null;
		selectedId: string | null;
		collapsed: ReadonlySet<string>;
		animate: boolean;
		onmove: (delta: number | 'first' | 'last') => void;
		onfocus: (node: TraceNode) => void;
		onselect: (node: TraceNode) => void;
		ontoggle: (node: TraceNode) => void;
	} = $props();

	let axis = $derived(ticks(v0, v1, 9));
	let nowPct = $derived(nowUs !== undefined ? clamp(pct(nowUs, v0, v1), 0, 100) : undefined);
	let effectiveFocus = $derived(focusedId ?? visible[0]?.id ?? null);

	function onkeydown(e: KeyboardEvent) {
		switch (e.key) {
			case 'ArrowDown':
				e.preventDefault();
				onmove(1);
				break;
			case 'ArrowUp':
				e.preventDefault();
				onmove(-1);
				break;
			case 'Home':
				e.preventDefault();
				onmove('first');
				break;
			case 'End':
				e.preventDefault();
				onmove('last');
				break;
		}
	}
</script>

<div class="waterfall">
	<div class="head" aria-hidden="true">
		<div class="hname">Service · operation</div>
		<div class="htl mono">
			{#each axis as t (t.us)}
				{@const x = pct(t.us, v0, v1)}
				<span class="tick" class:major={t.major} style="left: {x}%">{t.label}</span>
			{/each}
			{#if nowPct !== undefined}
				<span class="nowlabel" style="left: {nowPct}%">now</span>
			{/if}
		</div>
	</div>
	<div
		class="rows"
		role="tree"
		tabindex="-1"
		aria-label="Spans of trace {model.traceID}"
		{onkeydown}
	>
		{#each visible as n, i (n.id)}
			<SpanRow
				node={n}
				position={i + 1}
				size={visible.length}
				{v0}
				{v1}
				{nowUs}
				focused={effectiveFocus === n.id}
				selected={selectedId === n.id}
				collapsed={collapsed.has(n.id)}
				{animate}
				onfocus={() => onfocus(n)}
				onselect={() => onselect(n)}
				ontoggle={() => ontoggle(n)}
			/>
		{/each}
	</div>
</div>

<style>
	.waterfall {
		--name-col: minmax(220px, 32%);
		font-size: 0.8rem;
	}
	.head {
		display: grid;
		grid-template-columns: var(--name-col) 1fr;
		position: sticky;
		top: 0;
		z-index: 2;
		background: var(--panel);
		border-bottom: 1px solid var(--border);
	}
	.hname {
		padding: 0.35rem 0.5rem;
		font-size: 0.7rem;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--fg-dim);
		border-right: 1px solid var(--border);
	}
	.htl {
		position: relative;
		height: 1.75rem;
		overflow: hidden;
		font-size: 0.68rem;
		color: var(--fg-dim);
	}
	.tick {
		position: absolute;
		top: 0;
		bottom: 0;
		padding: 0.35rem 0 0 4px;
		border-left: 1px solid var(--border);
		white-space: nowrap;
	}
	.tick.major {
		color: var(--fg-muted);
		border-left-color: var(--fg-dim);
	}
	.nowlabel {
		position: absolute;
		top: 0;
		padding: 0.35rem 0 0 4px;
		border-left: 1px dashed var(--fg-dim);
		bottom: 0;
		color: var(--fg-muted);
	}
	.rows {
		outline: none;
	}
</style>
