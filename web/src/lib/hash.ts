// URL-hash state codec. Two shapes are accepted on read:
//   #trace?span=<span id>          (the API's span_url form)
//   #range=7d&layout=…&panel=<id>&span=<id>   (generic key=value pairs)
// Writes always use the generic form. Unknown keys are preserved.
export type HashState = Record<string, string>;

export function parseHash(hash: string): HashState {
	let h = hash.startsWith('#') ? hash.slice(1) : hash;
	const out: HashState = {};
	if (!h) return out;
	const q = h.indexOf('?');
	if (q >= 0) {
		// "#trace?span=x" — the part before "?" names the section; keep it under `_section`.
		const section = h.slice(0, q);
		if (section) out._section = section;
		h = h.slice(q + 1);
	}
	for (const [k, v] of new URLSearchParams(h)) {
		if (k) out[k] = v;
	}
	return out;
}

export function formatHash(state: HashState): string {
	const p = new URLSearchParams();
	for (const [k, v] of Object.entries(state)) {
		if (k === '_section' || v === '' || v === undefined) continue;
		p.set(k, v);
	}
	const s = p.toString();
	return s ? '#' + s : '';
}

/** Returns a copy of `state` with `patch` applied (undefined/'' removes a key). */
export function patchHash(state: HashState, patch: Record<string, string | undefined>): HashState {
	const next: HashState = { ...state };
	for (const [k, v] of Object.entries(patch)) {
		if (v === undefined || v === '') delete next[k];
		else next[k] = v;
	}
	delete next._section;
	return next;
}
