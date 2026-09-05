// Global toast stack (Grafana-like, bottom-right). Any module may push; the
// root layout mounts <Toasts/> once so a toast shows on every route. Toasts
// stay until dismissed or removed by their owner (firing alerts are removed
// when they resolve) unless a `ttlMs` is given.

export type ToastTone = 'info' | 'warning' | 'error' | 'success';

export interface ToastAction {
	label: string;
	/** Accessible name when the label alone is ambiguous ("Silence" → "Silence DivyAvailableForHire"). */
	ariaLabel?: string;
	href?: string;
	onclick?: () => void;
	primary?: boolean;
}

export interface Toast {
	id: string;
	title: string;
	body?: string;
	/** Small mono line under the title (severity, labels). */
	meta?: string;
	tone: ToastTone;
	actions: ToastAction[];
	ttlMs?: number;
	createdAt: number;
}

export type ToastInput = Omit<Toast, 'createdAt' | 'actions' | 'tone'> & {
	tone?: ToastTone;
	actions?: ToastAction[];
};

class ToastStore {
	items = $state<Toast[]>([]);
	private timers = new Map<string, ReturnType<typeof setTimeout>>();

	/** Adds (or replaces, by id) a toast. */
	push(t: ToastInput): Toast {
		const toast: Toast = { tone: 'info', actions: [], ...t, createdAt: Date.now() };
		const i = this.items.findIndex((x) => x.id === t.id);
		if (i >= 0) this.items[i] = toast;
		else this.items = [...this.items, toast];
		this.clearTimer(t.id);
		if (toast.ttlMs && toast.ttlMs > 0)
			this.timers.set(
				t.id,
				setTimeout(() => this.dismiss(t.id), toast.ttlMs)
			);
		return toast;
	}

	dismiss(id: string) {
		this.clearTimer(id);
		this.items = this.items.filter((x) => x.id !== id);
	}

	has(id: string): boolean {
		return this.items.some((x) => x.id === id);
	}

	clear() {
		for (const id of [...this.timers.keys()]) this.clearTimer(id);
		this.items = [];
	}

	private clearTimer(id: string) {
		const t = this.timers.get(id);
		if (t) clearTimeout(t);
		this.timers.delete(id);
	}
}

export const toasts = new ToastStore();
