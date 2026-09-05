// Global keyboard shortcut registry. One keydown listener (installed by the
// root layout) dispatches to scoped handlers; keys typed into inputs,
// textareas, selects or contenteditable elements are never intercepted, nor
// are chords with Ctrl/Meta/Alt. Later steps add `/`, `?` and the `promql` /
// Konami sequences here.
export type KeyHandler = (event: KeyboardEvent) => boolean | void;

const scopes = new Map<string, Map<string, KeyHandler>>();

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
