# web/ — the SvelteKit front end

SvelteKit 2 + Svelte 5 (runes) + TypeScript + Tailwind v4, built with
`@sveltejs/adapter-static` into `../internal/web/dist`, which the Go binary
embeds (`internal/web`, `//go:embed all:dist`). Every route is prerendered
against a running API except `/trace/[id]` (open id space → `200.html`
fallback, rendered by the client router).

## Loop

```
make dev            # API on :8080 (+ collectors) and Vite on :5173 (proxies /api, /metrics, /loki, /healthz, /readyz, /favicon.svg, /og, /robots.txt, /ascii)
make web-check      # svelte-check (0 errors, 0 warnings) + prettier --check
make web-gen        # schema/index.schema.json → src/lib/api/types.gen.ts (committed; make gen-check fails on drift)
make web-build      # prerender against a throwaway API, output in internal/web/dist
make web-e2e        # Playwright smoke suite against a fresh binary serving the embedded site
make web-shots      # reference screenshots (SHOTS_BASE_URL, default :5173)
```

Env (read from the repository-root `.env` or the process): `API_BASE` (where
server loads fetch during `vite dev`/prerender, default `http://127.0.0.1:8080`),
`PUBLIC_SITE_ORIGIN` (absolute og:/canonical origin; falls back to the origin
the API puts into `og_image` URLs), `PUBLIC_API_BASE` (browser API origin,
default same-origin).

## Layout

- `src/lib/api/client.ts` — typed client (`createApi`, browser `api`,
  `ApiError`) plus the only runtime narrowing helpers (`tagString`, `parseLinks`, …).
- `src/lib/api/types.gen.ts` — generated, never edited.
- `src/lib/server/api.ts` — `serverApi(fetch)` for `+*.server.ts` loads only.
- `src/lib/trace/` — `model.ts` (Jaeger JSON → tree, DFS order, open/TODO
  resolution) and `axis.ts` (ticks).
- `src/lib/components/trace/` — `TraceViewer` (state: viewport, collapse,
  focus, selection, `#span=` deep link) composed of `Minimap`, `Waterfall`,
  `SpanRow`, `VerticalTimeline` (≤ 640 px), `SpanDrawer`, `TraceIdBox`.
- `src/lib/state/*.svelte.ts` — theme (`dark|light|grafana2017`, localStorage
  `divy.theme`), motion (reduced), media (narrow/hydrated).
- `src/lib/keyboard.ts` — global shortcut registry (`bindKeys(scope, {...})`);
  `src/lib/hash.ts` — `#k=v` codec (also reads `#trace?span=`).
- `src/routes/(site)/` — prerendered pages with server loads
  (`+layout.server.ts`: profile, postmortems, siteOrigin); `src/routes/trace/[id]/`
  — no server load in its chain (fallback-served).

No prose about Divy lives here: every rendered string comes from the API;
literals are UI chrome only.
