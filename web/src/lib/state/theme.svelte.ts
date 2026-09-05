// Theme state: dark (default) | light | grafana2017 (Konami hook, styled in a
// later step). app.html applies the stored value before first paint; this
// store mirrors <html data-theme> and persists changes to localStorage.
export type Theme = 'dark' | 'light' | 'grafana2017';

const KEY = 'divy.theme';

function isTheme(v: unknown): v is Theme {
	return v === 'dark' || v === 'light' || v === 'grafana2017';
}

class ThemeState {
	current = $state<Theme>('dark');

	/** Reads the attribute app.html already set (call once after hydration). */
	sync() {
		if (typeof document === 'undefined') return;
		const t = document.documentElement.getAttribute('data-theme');
		if (isTheme(t)) this.current = t;
	}

	set(t: Theme) {
		this.current = t;
		if (typeof document === 'undefined') return;
		document.documentElement.setAttribute('data-theme', t);
		try {
			localStorage.setItem(KEY, t);
		} catch {
			// storage unavailable: the choice lasts for this page only
		}
	}

	/** Dark ↔ light. The 2017 theme is left by toggling too. */
	toggle() {
		this.set(this.current === 'light' ? 'dark' : 'light');
	}
}

export const theme = new ThemeState();
