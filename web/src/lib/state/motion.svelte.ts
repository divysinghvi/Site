// prefers-reduced-motion, readable from scripts (CSS gates its own animations).
class MotionState {
	reduced = $state(false);
	private mq: MediaQueryList | undefined;

	sync() {
		if (typeof window === 'undefined' || !window.matchMedia) return;
		this.mq ??= window.matchMedia('(prefers-reduced-motion: reduce)');
		this.reduced = this.mq.matches;
		this.mq.addEventListener('change', (e) => (this.reduced = e.matches));
	}
}

export const motion = new MotionState();
