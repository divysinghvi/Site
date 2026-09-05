<script lang="ts" module>
	export interface CellApi {
		dragStart: (e: PointerEvent) => void;
		resizeStart: (e: PointerEvent) => void;
		active: boolean;
	}
</script>

<script lang="ts" generics="T extends { id: string }">
	// The 24-column grid: cells are absolutely placed with percentage-based
	// positions (exact for 8 px margins, so the prerendered HTML is already
	// laid out with no measuring); pointer drag-to-rearrange and resize use
	// pixel maths with live collision push-down (grid.ts) and a snapped
	// placeholder. ≤ 640 px: CSS turns it into one column in (y, x) order and
	// dragging is disabled. Transitions are gated on prefers-reduced-motion.
	import type { Snippet } from 'svelte';
	import { onMount } from 'svelte';
	import { motion } from '$lib/state/motion.svelte';
	import {
		COLS,
		MARGIN,
		MIN_H,
		MIN_W,
		ROW_H,
		byYX,
		clampPos,
		colWidth,
		pixelHeight,
		pixelRect,
		place,
		rowsOf,
		samePos,
		snapX,
		snapY,
		type GridItem
	} from './grid';

	let {
		items,
		panels,
		narrow = false,
		onchange,
		cell
	}: {
		items: GridItem[];
		/** Panel definitions by id (what the cell snippet renders). */
		panels: ReadonlyMap<string, T>;
		/** Disables dragging (the phone layout). */
		narrow?: boolean;
		/** Called with the committed layout after a drop / resize end. */
		onchange: (items: GridItem[]) => void;
		cell: Snippet<[T, CellApi]>;
	} = $props();

	let host = $state<HTMLDivElement | null>(null);
	let width = $state(0);
	let colW = $derived(colWidth(width));
	let preview = $state<GridItem[] | null>(null);
	let active = $state<{
		id: string;
		mode: 'move' | 'resize';
		left: number;
		top: number;
		w: number;
		h: number;
	} | null>(null);
	let shown = $derived([...(preview ?? items)].sort(byYX));
	let rows = $derived(rowsOf(shown) + (active ? 2 : 0));

	onMount(() => {
		const ro = new ResizeObserver((entries) => {
			const r = entries[0]?.contentRect;
			if (r) width = r.width;
		});
		if (host) ro.observe(host);
		return () => ro.disconnect();
	});

	interface Drag {
		id: string;
		mode: 'move' | 'resize';
		startX: number;
		startY: number;
		origin: GridItem;
		rect: { left: number; top: number; width: number; height: number };
		moved: boolean;
	}
	let drag: Drag | null = null;

	function begin(e: PointerEvent, id: string, mode: 'move' | 'resize') {
		if (narrow || e.button !== 0) return;
		const it = items.find((i) => i.id === id);
		if (!it || !host) return;
		e.preventDefault();
		width = host.getBoundingClientRect().width;
		const cw = colWidth(width);
		const rect = pixelRect(it, cw);
		drag = {
			id,
			mode,
			startX: e.clientX,
			startY: e.clientY,
			origin: { ...it },
			rect,
			moved: false
		};
		active = { id, mode, left: rect.left, top: rect.top, w: rect.width, h: rect.height };
		preview = items.map((i) => ({ ...i }));
		window.addEventListener('pointermove', onMove);
		window.addEventListener('pointerup', onUp);
		window.addEventListener('pointercancel', onUp);
		document.body.style.userSelect = 'none';
	}

	function onMove(e: PointerEvent) {
		if (!drag || !active) return;
		const dx = e.clientX - drag.startX;
		const dy = e.clientY - drag.startY;
		if (Math.abs(dx) + Math.abs(dy) > 2) drag.moved = true;
		let next: GridItem;
		if (drag.mode === 'move') {
			const left = Math.max(0, drag.rect.left + dx);
			const top = Math.max(0, drag.rect.top + dy);
			active = { ...active, left, top };
			next = { ...drag.origin, x: snapX(left, colW), y: snapY(top) };
		} else {
			const w = Math.max(MIN_W * colW, drag.rect.width + dx);
			const h = Math.max(MIN_H * ROW_H, drag.rect.height + dy);
			active = { ...active, w, h };
			next = {
				...drag.origin,
				w: Math.max(MIN_W, Math.round((w + MARGIN) / (colW + MARGIN))),
				h: Math.max(MIN_H, Math.round((h + MARGIN) / (ROW_H + MARGIN)))
			};
		}
		const clamped = { ...next, ...clampPos(next) };
		const cur = (preview ?? items).find((i) => i.id === drag!.id);
		if (cur && samePos(cur, clamped)) return;
		preview = place(preview ?? items, drag.id, clamped);
	}

	function onUp() {
		window.removeEventListener('pointermove', onMove);
		window.removeEventListener('pointerup', onUp);
		window.removeEventListener('pointercancel', onUp);
		document.body.style.userSelect = '';
		const committed = preview;
		const moved = drag?.moved;
		drag = null;
		active = null;
		preview = null;
		if (committed && moved) onchange(committed);
	}

	/** Percentage placement: colW + margin = (100% + 8px) / 24. */
	function styleFor(it: GridItem): string {
		if (active && active.id === it.id) {
			return `left:${active.left}px;top:${active.top}px;width:${active.w}px;height:${active.h}px`;
		}
		const unit = `(100% + ${MARGIN}px) / ${COLS}`;
		return (
			`left:calc(${it.x} * ${unit});` +
			`top:${it.y * (ROW_H + MARGIN)}px;` +
			`width:calc(${it.w} * ${unit} - ${MARGIN}px);` +
			`height:${pixelHeight(it.h)}px;` +
			`--narrow-h:${pixelHeight(Math.min(it.h, 10))}px`
		);
	}

	let placeholder = $derived.by(() => {
		if (!active || !preview) return null;
		const it = preview.find((i) => i.id === active!.id);
		return it ? pixelRect(it, colW) : null;
	});
</script>

<div
	class="grid"
	class:animate={!motion.reduced && !active}
	class:dragging={!!active}
	bind:this={host}
	style:height="{Math.max(1, rows) * (ROW_H + MARGIN) - MARGIN}px"
>
	{#if placeholder}
		<div
			class="placeholder"
			aria-hidden="true"
			style:left="{placeholder.left}px"
			style:top="{placeholder.top}px"
			style:width="{placeholder.width}px"
			style:height="{placeholder.height}px"
		></div>
	{/if}
	{#each shown as it (it.id)}
		{@const p = panels.get(it.id)}
		{#if p}
			<div
				class="cell"
				class:active={active?.id === it.id}
				class:resizing={active?.id === it.id && active.mode === 'resize'}
				style={styleFor(it)}
			>
				{@render cell(p, {
					dragStart: (e) => begin(e, it.id, 'move'),
					resizeStart: (e) => begin(e, it.id, 'resize'),
					active: active?.id === it.id
				})}
			</div>
		{/if}
	{/each}
</div>

<style>
	.grid {
		position: relative;
		width: 100%;
	}
	.cell {
		position: absolute;
		box-sizing: border-box;
	}
	.animate .cell {
		transition:
			left 180ms cubic-bezier(0.2, 0.7, 0.2, 1),
			top 180ms cubic-bezier(0.2, 0.7, 0.2, 1),
			width 180ms cubic-bezier(0.2, 0.7, 0.2, 1),
			height 180ms cubic-bezier(0.2, 0.7, 0.2, 1);
	}
	.cell.active {
		z-index: 10;
		opacity: 0.92;
		box-shadow: 0 12px 32px rgba(0, 0, 0, 0.45);
		cursor: grabbing;
	}
	.cell.resizing {
		cursor: nwse-resize;
	}
	.dragging .cell:not(.active) {
		transition:
			left 120ms ease-out,
			top 120ms ease-out;
	}
	.placeholder {
		position: absolute;
		border: 1px dashed var(--blue);
		border-radius: 6px;
		background: color-mix(in srgb, var(--blue) 10%, transparent);
	}
	/* phone: one column, file order, drag off (the page also disables it) */
	@media (max-width: 639.98px) {
		.grid {
			display: flex;
			flex-direction: column;
			gap: 8px;
			height: auto !important;
		}
		.cell {
			position: static !important;
			width: 100% !important;
			height: var(--narrow-h) !important;
			left: auto !important;
			top: auto !important;
			transition: none;
		}
		.placeholder {
			display: none;
		}
	}
</style>
