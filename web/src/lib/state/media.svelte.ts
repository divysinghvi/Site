// Viewport state. `narrow` (≤ 640 px) switches the trace to the vertical
// timeline; `hydrated` lets components render both layouts in the static HTML
// (CSS decides) and a single one once the browser knows its width.
export const NARROW_MAX = 639.98; // Tailwind `sm` starts at 640px

class MediaState {
	narrow = $state(false);
	hydrated = $state(false);
	private mq: MediaQueryList | undefined;

	sync() {
		if (typeof window === 'undefined' || !window.matchMedia) return;
		this.mq ??= window.matchMedia(`(max-width: ${NARROW_MAX}px)`);
		this.narrow = this.mq.matches;
		this.mq.addEventListener('change', (e) => (this.narrow = e.matches));
		this.hydrated = true;
	}
}

export const media = new MediaState();
