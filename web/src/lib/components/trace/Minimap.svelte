<script lang="ts">
	// Time-axis overview with a draggable brush: drag on empty space to select
	// a window, drag the window to pan, drag its edges to resize, double-click
	// to reset. The two range inputs give the same control to keyboard users.
	import type { TraceModel } from '$lib/trace/model';
	import { ticks, pct, clamp } from '$lib/trace/axis';
	import { formatDateAt } from '$lib/format';

	let {
		model,
		v0,
		v1,
		onchange
	}: {
		model: TraceModel;
		v0: number;
		v1: number;
		onchange: (v0: number, v1: number) => void;
	} = $props();

	const W = 1000;
	const H = 56;
	const STEPS = 1000;

	let el = $state<SVGSVGElement | null>(null);
	let span = $derived(model.endUs - model.startUs);
	let x0 = $derived(clamp(pct(v0, model.startUs, model.endUs), 0, 100) * 10);
	let x1 = $derived(clamp(pct(v1, model.startUs, model.endUs), 0, 100) * 10);
	let rowH = $derived(Math.max(1.5, Math.min(4, (H - 8) / Math.max(model.nodes.length, 1))));
	let axis = $derived(ticks(model.startUs, model.endUs, 6));
	let zoomed = $derived(v0 > model.startUs || v1 < model.endUs);
	let minWindow = $derived(span / STEPS);

	function toUs(clientX: number): number {
		if (!el) return model.startUs;
		const rect = el.getBoundingClientRect();
		const f = clamp((clientX - rect.left) / Math.max(rect.width, 1), 0, 1);
		return model.startUs + f * span;
	}

	function commit(a: number, b: number) {
		let lo = Math.min(a, b);
		let hi = Math.max(a, b);
		if (hi - lo < minWindow) hi = lo + minWindow;
		lo = clamp(lo, model.startUs, model.endUs - minWindow);
		hi = clamp(hi, lo + minWindow, model.endUs);
		onchange(lo, hi);
	}

	type Drag = { kind: 'new' | 'pan' | 'left' | 'right'; anchor: number; v0: number; v1: number };
	let drag: Drag | null = null;

	function down(e: PointerEvent, kind: Drag['kind']) {
		if (e.button !== 0) return;
		e.preventDefault();
		e.stopPropagation();
		drag = { kind, anchor: toUs(e.clientX), v0, v1 };
		(e.currentTarget as Element).setPointerCapture?.(e.pointerId);
		window.addEventListener('pointermove', move);
		window.addEventListener('pointerup', up, { once: true });
	}

	function move(e: PointerEvent) {
		if (!drag) return;
		const t = toUs(e.clientX);
		switch (drag.kind) {
			case 'new':
				commit(drag.anchor, t);
				break;
			case 'pan': {
				const w = drag.v1 - drag.v0;
				let lo = drag.v0 + (t - drag.anchor);
				lo = clamp(lo, model.startUs, model.endUs - w);
				onchange(lo, lo + w);
				break;
			}
			case 'left':
				commit(t, drag.v1);
				break;
			case 'right':
				commit(drag.v0, t);
				break;
		}
	}

	function up() {
		drag = null;
		window.removeEventListener('pointermove', move);
	}

	function reset() {
		onchange(model.startUs, model.endUs);
	}

	// keyboard sliders (0..STEPS)
	let s0 = $derived(Math.round(((v0 - model.startUs) / span) * STEPS));
	let s1 = $derived(Math.round(((v1 - model.startUs) / span) * STEPS));
	function fromSlider(kind: 'left' | 'right', value: number) {
		const t = model.startUs + (value / STEPS) * span;
		if (kind === 'left') commit(Math.min(t, v1 - minWindow), v1);
		else commit(v0, Math.max(t, v0 + minWindow));
	}
</script>

<div class="minimap" aria-label="Time axis overview">
	<svg
		bind:this={el}
		viewBox="0 0 {W} {H}"
		preserveAspectRatio="none"
		class="map"
		role="img"
		aria-label="Overview of every span; drag to zoom, double-click to reset"
		onpointerdown={(e) => down(e, 'new')}
		ondblclick={reset}
	>
		<rect x="0" y="0" width={W} height={H} class="bg" />
		{#each axis as t (t.us)}
			{@const x = pct(t.us, model.startUs, model.endUs) * 10}
			<line x1={x} x2={x} y1="0" y2={H} class="grid" class:major={t.major} />
		{/each}
		{#each model.nodes as n (n.id)}
			{@const a = clamp(pct(n.startUs, model.startUs, model.endUs), 0, 100) * 10}
			{@const b = clamp(pct(n.endUs, model.startUs, model.endUs), 0, 100) * 10}
			<rect
				x={a}
				y={4 + n.index * rowH}
				width={Math.max(b - a, 1.5)}
				height={Math.max(rowH - 0.5, 1)}
				fill={n.color}
				opacity={n.todoDates ? 0.45 : 0.9}
			/>
		{/each}
		{#if model.nowUs !== undefined}
			{@const x = pct(model.nowUs, model.startUs, model.endUs) * 10}
			<line x1={x} x2={x} y1="0" y2={H} class="now" />
		{/if}
		<rect x="0" y="0" width={x0} height={H} class="shade" />
		<rect x={x1} y="0" width={W - x1} height={H} class="shade" />
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<rect
			x={x0}
			y="0"
			width={Math.max(x1 - x0, 2)}
			height={H}
			class="brush"
			onpointerdown={(e) => down(e, 'pan')}
		/>
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<rect
			x={x0 - 6}
			y="0"
			width="12"
			height={H}
			class="handle"
			onpointerdown={(e) => down(e, 'left')}
		/>
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<rect
			x={x1 - 6}
			y="0"
			width="12"
			height={H}
			class="handle"
			onpointerdown={(e) => down(e, 'right')}
		/>
	</svg>
	<div class="axis mono" aria-hidden="true">
		{#each axis as t (t.us)}
			<span style="left: {pct(t.us, model.startUs, model.endUs)}%">{t.label}</span>
		{/each}
	</div>
	<div class="sliders">
		<label>
			<span class="sr-only">Zoom window start</span>
			<input
				type="range"
				min="0"
				max={STEPS}
				step="1"
				value={s0}
				aria-valuetext={formatDateAt(undefined, span > 3e13 ? 'day' : 'exact', v0)}
				oninput={(e) => fromSlider('left', Number((e.currentTarget as HTMLInputElement).value))}
			/>
		</label>
		<label>
			<span class="sr-only">Zoom window end</span>
			<input
				type="range"
				min="0"
				max={STEPS}
				step="1"
				value={s1}
				aria-valuetext={formatDateAt(undefined, span > 3e13 ? 'day' : 'exact', v1)}
				oninput={(e) => fromSlider('right', Number((e.currentTarget as HTMLInputElement).value))}
			/>
		</label>
		{#if zoomed}
			<button type="button" class="btn" onclick={reset}>Reset zoom</button>
		{/if}
	</div>
</div>

<style>
	.minimap {
		display: flex;
		flex-direction: column;
		gap: 2px;
		padding: 0.5rem 0.75rem 0.25rem;
		border-bottom: 1px solid var(--border);
		user-select: none;
	}
	.map {
		width: 100%;
		height: 56px;
		display: block;
		cursor: crosshair;
		border-radius: 3px;
		touch-action: none;
	}
	.bg {
		fill: var(--bg);
	}
	.grid {
		stroke: var(--border);
		stroke-width: 1;
		vector-effect: non-scaling-stroke;
	}
	.grid.major {
		stroke: var(--fg-dim);
	}
	.now {
		stroke: var(--fg-muted);
		stroke-width: 1;
		stroke-dasharray: 3 3;
		vector-effect: non-scaling-stroke;
	}
	.shade {
		fill: var(--bg);
		opacity: 0.55;
		pointer-events: none;
	}
	.brush {
		fill: var(--blue);
		fill-opacity: 0.12;
		stroke: var(--blue);
		stroke-width: 1;
		vector-effect: non-scaling-stroke;
		cursor: grab;
	}
	.handle {
		fill: var(--blue);
		fill-opacity: 0;
		cursor: ew-resize;
	}
	.handle:hover {
		fill-opacity: 0.35;
	}
	.axis {
		position: relative;
		height: 1rem;
		font-size: 0.65rem;
		color: var(--fg-dim);
	}
	.axis span {
		position: absolute;
		transform: translateX(-50%);
		white-space: nowrap;
	}
	.axis span:first-child {
		transform: none;
	}
	.sliders {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}
	.sliders label {
		flex: 1;
		display: flex;
	}
	.sliders input[type='range'] {
		width: 100%;
		height: 1.25rem;
		accent-color: var(--blue);
	}
</style>
