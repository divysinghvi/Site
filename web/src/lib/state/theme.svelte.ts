// Theme state: dark (default) | light | grafana2017 (the Konami code).
// app.html applies the stored value before first paint; this store mirrors
// <html data-theme> and persists changes: dark/light to localStorage, the
// 2017 theme to sessionStorage (it lasts for the browser session only).
export type Theme = 'dark' | 'light' | 'grafana2017';

const KEY = 'divy.theme';
export const SESSION_KEY = 'divy.theme.session';

/** `<meta name="theme-color">` per theme (the --bg token of app.css). */
export const THEME_COLOR: Record<Theme, string> = {
	dark: '#0b0c0e',
	light: '#f4f5f5',
	grafana2017: '#0f1926'
};

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
		document.querySelector('meta[name="theme-color"]')?.setAttribute('content', THEME_COLOR[t]);
		try {
			if (t === 'grafana2017') sessionStorage.setItem(SESSION_KEY, t);
			else {
				sessionStorage.removeItem(SESSION_KEY);
				localStorage.setItem(KEY, t);
			}
		} catch {
			// storage unavailable: the choice lasts for this page only
		}
	}

	/** Dark ↔ light. The 2017 theme is left by toggling too. */
	toggle() {
		this.set(this.current === 'light' ? 'dark' : 'light');
	}

	/** The Konami code: into the 2017 theme, or back to the stored dark/light choice. */
	konami() {
		if (this.current === 'grafana2017') {
			let back: Theme = 'dark';
			try {
				if (localStorage.getItem(KEY) === 'light') back = 'light';
			} catch {
				// keep dark
			}
			this.set(back);
		} else this.set('grafana2017');
	}
}

export const theme = new ThemeState();
