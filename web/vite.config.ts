import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

/// <reference types="node" />
// The Go API the dev server proxies to (API_BASE, default :8080). Only paths
// the Go binary owns are proxied; everything else is SvelteKit.
const api = process.env.API_BASE || 'http://127.0.0.1:8080';
const proxied = [
	'/api',
	'/metrics',
	'/loki',
	'/healthz',
	'/readyz',
	'/favicon.svg',
	'/og',
	'/robots.txt',
	'/ascii'
];

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	server: {
		host: '127.0.0.1',
		port: 5173,
		strictPort: true,
		proxy: Object.fromEntries(proxied.map((p) => [p, { target: api, changeOrigin: false }]))
	},
	preview: { port: 4173, strictPort: true }
});
