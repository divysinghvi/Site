import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

// Paths the Go binary serves itself. The prerender crawler follows every href
// it finds in the rendered HTML; these are not SvelteKit routes, so a 404 from
// the SvelteKit side during prerender is expected and must not fail the build.
const apiOwned = [
	'/api/',
	'/loki/',
	'/metrics',
	'/healthz',
	'/readyz',
	'/favicon.svg',
	'/favicon.ico',
	'/og/',
	'/robots.txt',
	'/ascii',
	'/sitemap.xml'
];

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	kit: {
		adapter: adapter({
			// Embedded by internal/web (//go:embed all:dist). Only dist/.gitkeep is committed.
			pages: '../internal/web/dist',
			assets: '../internal/web/dist',
			// /trace/[id] is not prerendered (open id space): the Go static handler
			// serves 200.html for unknown non-API paths and the client router renders it.
			fallback: '200.html',
			precompress: false,
			strict: true
		}),
		// One .env at the repository root serves Go, web and compose; the web
		// build sees only API_* (server loads during prerender) and PUBLIC_*.
		env: { dir: '..', publicPrefix: 'PUBLIC_', privatePrefix: 'API_' },
		prerender: {
			concurrency: 4,
			entries: ['*', '/404'],
			handleHttpError: ({ path, message }) => {
				if (apiOwned.some((p) => path === p || path.startsWith(p))) return;
				throw new Error(message);
			},
			// `#panel=<id>` / `#layout=…` are dashboard state (read by the page, not
			// element ids); every other missing fragment still fails the build.
			handleMissingId: ({ id, message }) => {
				if (/^[a-z]+=/.test(id)) return;
				throw new Error(message);
			}
		}
	}
};

export default config;
