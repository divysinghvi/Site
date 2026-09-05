// Dashboard URL-hash state: `#range=7d&layout=<base64url JSON>&refresh=off&panel=<id>`.
// `layout` holds only the panels whose gridPos differs from content/panels.yaml
// (`{id: {x,y,w,h}}`), so the file stays the default and a shared link
// restores the same arrangement for anyone (brief §5). Built on $lib/hash
// (parseHash/formatHash keep unknown keys and the `#trace?span=` form).
import type { GridPos } from '$lib/api/types.gen';
import { parseHash, formatHash, patchHash, type HashState } from '$lib/hash';
import { isPreset, type Preset } from '$lib/timerange';

export type LayoutOverrides = Record<string, GridPos>;

export interface DashboardHash {
	range?: Preset;
	layout: LayoutOverrides;
	/** `off` disables auto-refresh; absent = on. */
	refresh?: 'off';
	/** Panel id to scroll to and flash (alerts' runbook_url form). */
	panel?: string;
	/** Everything else, preserved verbatim. */
	rest: HashState;
}

function isGridPos(v: unknown): v is GridPos {
	if (!v || typeof v !== 'object') return false;
	const o = v as Record<string, unknown>;
	return (
		Number.isInteger(o.x) &&
		Number.isInteger(o.y) &&
		Number.isInteger(o.w) &&
		Number.isInteger(o.h) &&
		(o.x as number) >= 0 &&
		(o.y as number) >= 0 &&
		(o.w as number) >= 1 &&
		(o.h as number) >= 1
	);
}

export function encodeLayout(overrides: LayoutOverrides): string {
	const ids = Object.keys(overrides).sort();
	if (ids.length === 0) return '';
	const compact: Record<string, [number, number, number, number]> = {};
	for (const id of ids) {
		const g = overrides[id]!;
		compact[id] = [g.x, g.y, g.w, g.h];
	}
	return base64url(JSON.stringify(compact));
}

export function decodeLayout(s: string | undefined): LayoutOverrides {
	const out: LayoutOverrides = {};
	if (!s) return out;
	let parsed: unknown;
	try {
		parsed = JSON.parse(fromBase64url(s));
	} catch {
		return out;
	}
	if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return out;
	for (const [id, v] of Object.entries(parsed as Record<string, unknown>)) {
		if (Array.isArray(v) && v.length === 4) {
			const g = { x: v[0], y: v[1], w: v[2], h: v[3] };
			if (isGridPos(g)) out[id] = g;
		} else if (isGridPos(v)) {
			out[id] = { x: v.x, y: v.y, w: v.w, h: v.h };
		}
	}
	return out;
}

export function parseDashboardHash(hash: string): DashboardHash {
	const st = parseHash(hash);
	const { range, layout, refresh, panel, ...rest } = st;
	delete rest._section;
	return {
		range: isPreset(range) ? range : undefined,
		layout: decodeLayout(layout),
		refresh: refresh === 'off' ? 'off' : undefined,
		panel: panel || undefined,
		rest
	};
}

export function formatDashboardHash(d: DashboardHash): string {
	const st = patchHash(d.rest, {
		range: d.range,
		layout: encodeLayout(d.layout),
		refresh: d.refresh,
		panel: d.panel
	});
	return formatHash(st);
}

/** Only the panels whose position differs from the file. */
export function diffLayout(base: Record<string, GridPos>, current: Record<string, GridPos>) {
	const out: LayoutOverrides = {};
	for (const [id, g] of Object.entries(current)) {
		const b = base[id];
		if (!b) continue;
		if (b.x !== g.x || b.y !== g.y || b.w !== g.w || b.h !== g.h) out[id] = { ...g };
	}
	return out;
}

// base64url without padding, UTF-8 safe (the JSON is ASCII anyway).
function base64url(s: string): string {
	const bytes = new TextEncoder().encode(s);
	let bin = '';
	for (const b of bytes) bin += String.fromCharCode(b);
	return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function fromBase64url(s: string): string {
	const b64 = s.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (s.length % 4)) % 4);
	const bin = atob(b64);
	const bytes = new Uint8Array(bin.length);
	for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
	return new TextDecoder().decode(bytes);
}
