// Server-only client used by +page.server.ts / +layout.server.ts loads. These
// run at build time (prerender) and in `vite dev`; API_BASE says where the Go
// API listens (default :8080). Never imported by browser code.
import { env } from '$env/dynamic/private';
import { createApi, type Api, type Fetch } from '$lib/api/client';

export const API_BASE = env.API_BASE || 'http://127.0.0.1:8080';

export function serverApi(fetch: Fetch): Api {
	return createApi({ fetch, base: API_BASE });
}
