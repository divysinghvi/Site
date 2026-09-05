// The 24-column dashboard grid (Grafana constants: 30 px rows, 8 px margins).
// Pure layout math: pixel rects, collision push-down and upward compaction
// (gridstack's float:false behaviour), keyboard move/resize. No DOM here.
import type { GridPos } from '$lib/api/types.gen';

export const COLS = 24;
export const ROW_H = 30;
export const MARGIN = 8;
export const MIN_W = 3;
export const MIN_H = 2;

export interface GridItem extends GridPos {
	id: string;
}

export function byYX(a: GridPos, b: GridPos): number {
	return a.y - b.y || a.x - b.x;
}

export function overlaps(a: GridPos, b: GridPos): boolean {
	return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
}

export function clampPos(g: GridPos, cols = COLS): GridPos {
	const w = Math.min(cols, Math.max(MIN_W, Math.round(g.w)));
	const h = Math.max(MIN_H, Math.round(g.h));
	const x = Math.min(cols - w, Math.max(0, Math.round(g.x)));
	const y = Math.max(0, Math.round(g.y));
	return { x, y, w, h };
}

export function samePos(a: GridPos, b: GridPos): boolean {
	return a.x === b.x && a.y === b.y && a.w === b.w && a.h === b.h;
}

/**
 * Returns a new layout with `fixedId` kept where it is, every other item
 * pushed below anything it overlaps, then everything compacted upward in
 * (y, x) order. Terminates: each push only moves items downward.
 */
export function resolve(items: readonly GridItem[], fixedId?: string): GridItem[] {
	const list = items.map((i) => ({ ...i }));
	if (fixedId) {
		const fixed = list.find((i) => i.id === fixedId);
		if (fixed) {
			const placed: GridItem[] = [fixed];
			for (const o of list.filter((i) => i.id !== fixedId).sort(byYX)) {
				let guard = 0;
				for (;;) {
					const hits = placed.filter((p) => overlaps(p, o));
					if (hits.length === 0 || guard++ > 500) break;
					o.y = Math.max(...hits.map((p) => p.y + p.h));
				}
				placed.push(o);
			}
		}
	}
	const done: GridItem[] = [];
	for (const it of [...list].sort(byYX)) {
		let y = it.y;
		while (y > 0) {
			const probe = { ...it, y: y - 1 };
			if (done.some((d) => overlaps(d, probe))) break;
			y--;
		}
		it.y = y;
		done.push(it);
	}
	return list;
}

/** Applies a new position to one item and resolves the rest around it. */
export function place(
	items: readonly GridItem[],
	id: string,
	pos: GridPos,
	cols = COLS
): GridItem[] {
	const next = clampPos(pos, cols);
	return resolve(
		items.map((i) => (i.id === id ? { ...i, ...next } : i)),
		id
	);
}

/**
 * Keyboard equivalents (menu items). Horizontal moves and resizes step one
 * cell; vertical moves swap with the nearest panel above/below in the same
 * columns (in a gravity-up grid "one row down" would compact straight back).
 */
export function nudge(
	items: readonly GridItem[],
	id: string,
	dx: number,
	dy: number,
	dw = 0,
	dh = 0,
	cols = COLS
): GridItem[] {
	const it = items.find((i) => i.id === id);
	if (!it) return [...items];
	let y = it.y;
	if (dy !== 0) {
		const sameCols = (o: GridItem) => o.id !== id && o.x < it.x + it.w && it.x < o.x + o.w;
		if (dy > 0) {
			const below = items
				.filter((o) => sameCols(o) && o.y >= it.y + it.h)
				.sort((a, b) => a.y - b.y)[0];
			y = below ? below.y + below.h : it.y + dy;
		} else {
			const above = items
				.filter((o) => sameCols(o) && o.y + o.h <= it.y)
				.sort((a, b) => b.y + b.h - (a.y + a.h))[0];
			y = above ? above.y : Math.max(0, it.y + dy);
		}
	}
	return place(items, id, { x: it.x + dx, y, w: it.w + dw, h: it.h + dh }, cols);
}

/** Number of rows the layout occupies. */
export function rowsOf(items: readonly GridPos[]): number {
	return items.reduce((m, i) => Math.max(m, i.y + i.h), 0);
}

export function colWidth(containerWidth: number, cols = COLS): number {
	return Math.max(0, (containerWidth - (cols - 1) * MARGIN) / cols);
}

export function pixelRect(g: GridPos, colW: number) {
	return {
		left: g.x * (colW + MARGIN),
		top: g.y * (ROW_H + MARGIN),
		width: g.w * colW + (g.w - 1) * MARGIN,
		height: g.h * ROW_H + (g.h - 1) * MARGIN
	};
}

/** Pixel height of a panel (also used by the single-column phone layout). */
export function pixelHeight(h: number): number {
	return h * ROW_H + (h - 1) * MARGIN;
}

/** Nearest grid cell for a pixel offset (drag snapping). */
export function snapX(px: number, colW: number): number {
	return Math.round(px / (colW + MARGIN));
}

export function snapY(px: number): number {
	return Math.round(px / (ROW_H + MARGIN));
}
