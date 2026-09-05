// Global keyboard shortcut registry. One keydown listener (installed by the
// root layout) dispatches to scoped handlers; keys typed into inputs,
// textareas, selects or contenteditable elements are never intercepted, nor
// are chords with Ctrl/Meta/Alt. Key sequences (`promql` opens the console,
// the Konami code switches the theme) are tracked by the same listener.
export type KeyHandler = (event: KeyboardEvent) => boolean | void;

const scopes = new Map<string, Map<string, KeyHandler>>();

interface Sequence {
	keys: readonly string[];
	handler: () => void;
	/** How many keys of the sequence have been typed in a row. */
	at: number;
}

const sequences = new Map<string, Sequence>();

/** The Konami code as KeyboardEvent.key values (letters compared case-insensitively). */
export const KONAMI = [
	'ArrowUp',
	'ArrowUp',
	'ArrowDown',
	'ArrowDown',
	'ArrowLeft',
	'ArrowRight',
	'ArrowLeft',
	'ArrowRight',
	'b',
	'a'
] as const;

/**
 * Registers a key sequence typed outside editable elements (single keys, no
 * modifiers); `handler` runs when the last key lands. Returns the unbind function.
 */
export function bindSequence(
	name: string,
	keys: readonly string[],
	handler: () => void
): () => void {
	sequences.set(name, { keys, handler, at: 0 });
	return () => {
		sequences.delete(name);
	};
}

function sameKey(a: string, b: string): boolean {
	return a.length === 1 && b.length === 1 ? a.toLowerCase() === b.toLowerCase() : a === b;
}

/** Feeds one key to every sequence; true when one completed (its handler ran). */
function advanceSequences(key: string): boolean {
	let fired = false;
	for (const s of sequences.values()) {
		if (sameKey(key, s.keys[s.at]!)) s.at++;
		else s.at = sameKey(key, s.keys[0]!) ? 1 : 0;
		if (s.at >= s.keys.length) {
			s.at = 0;
			fired = true;
			s.handler();
		}
	}
	return fired;
}

export function isEditable(target: EventTarget | null): boolean {
	if (!(target instanceof HTMLElement)) return false;
	const tag = target.tagName;
	return (
		tag === 'INPUT' ||
		tag === 'TEXTAREA' ||
		tag === 'SELECT' ||
		target.isContentEditable ||
		target.closest('[data-keyboard-ignore]') !== null
	);
}

/** Registers handlers for a scope; returns the unbind function. Keys are KeyboardEvent.key values. */
export function bindKeys(scope: string, keys: Record<string, KeyHandler>): () => void {
	const map = scopes.get(scope) ?? new Map<string, KeyHandler>();
	for (const [k, h] of Object.entries(keys)) map.set(k, h);
	scopes.set(scope, map);
	return () => {
		for (const k of Object.keys(keys)) map.delete(k);
		if (map.size === 0) scopes.delete(scope);
	};
}

function dispatch(event: KeyboardEvent) {
	if (event.defaultPrevented) return;
	if (event.ctrlKey || event.metaKey || event.altKey) return;
	if (isEditable(event.target)) return;
	if (advanceSequences(event.key)) {
		event.preventDefault();
		return;
	}
	for (const map of scopes.values()) {
		const h = map.get(event.key);
		if (h && h(event) !== false) {
			event.preventDefault();
			return;
		}
	}
}

/** Installs the listener on `target`; returns the uninstall function. */
export function installKeyboard(target: Window | Document = window): () => void {
	const listener = (e: Event) => dispatch(e as KeyboardEvent);
	target.addEventListener('keydown', listener);
	return () => target.removeEventListener('keydown', listener);
}
