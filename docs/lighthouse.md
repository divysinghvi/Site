# Lighthouse

Real numbers from one run per page against the embedded site served by a fresh binary
(`go build -o /tmp/divy-w5 ./cmd/api`, `internal/web/dist` from `npm run build`, file
database, no GitHub token, manual metrics collected once). Recorded 2026-09-05.

## Command

```sh
# web/ — Lighthouse 13.4.1 via npx, the Playwright Chromium already installed
export CHROME_PATH=/opt/pw-browsers/chromium-1194/chrome-linux/chrome
npx --yes lighthouse@13.4.1 http://127.0.0.1:8080/ \
  --chrome-flags="--headless --no-sandbox" --output=json --output-path=lh-home.json --quiet
npx --yes lighthouse@13.4.1 http://127.0.0.1:8080/dashboard \
  --chrome-flags="--headless --no-sandbox" --output=json --output-path=lh-dashboard.json --quiet
# same two commands with --preset=desktop for the desktop rows
```

The binary was started as
`RATE_LIMIT_RPS=1000 RATE_LIMIT_BURST=5000 /tmp/divy-w5 serve --addr 127.0.0.1:8080 --db file:/tmp/divy-w5.db --content ./content --site-origin http://127.0.0.1:8080`
(the per-IP limiter's defaults, 20 rps / burst 100, are for real traffic; Lighthouse and
a 4-worker Playwright run from one IP trip them).

## Scores

| Page         | Form factor (Lighthouse default = mobile, simulated slow 4G) | Performance | Accessibility | Best practices | SEO |
| ------------ | ------------------------------------------------------------ | ----------: | ------------: | -------------: | --: |
| `/`          | mobile                                                       |      **80** |           100 |            100 | 100 |
| `/dashboard` | mobile                                                       |      **77** |           100 |            100 | 100 |
| `/`          | desktop (`--preset=desktop`)                                 |          99 |           100 |            100 | 100 |
| `/dashboard` | desktop (`--preset=desktop`)                                 |          98 |           100 |            100 | 100 |

Mobile metrics: `/` FCP 3.8 s · LCP 3.8 s · TBT 80 ms · CLS 0.001 · SI 3.8 s;
`/dashboard` FCP 3.5 s · LCP 4.3 s · TBT 70 ms · CLS 0.019 · SI 3.5 s.
Desktop: `/` FCP 0.8 s · LCP 0.8 s · TBT 0 ms; `/dashboard` FCP 0.8 s · LCP 1.0 s · TBT 0 ms.

Lighthouse 13 also reports an "agentic browsing" category (67: no `/llms.txt`); it is not
one of the brief's four.

## What holds the mobile performance score down (from the reports)

- **No response compression.** The document is 157 KB (prerendered hero trace + the
  inline hydration data) and is served identity-encoded; the CSS (29 KB + 12 KB) and the
  main JS chunk (52 KB) likewise. `document-latency-insight` estimates 103 KB saved on the
  HTML alone. The Go static handler does not gzip/brotli; on Vercel the edge compresses
  function responses for clients that accept it, so production numbers will be better than
  this local run — measure again after deploy. Adding gzip to the static handler is the one
  change that would move this score most (API lane).
- **Render-blocking CSS** (660 ms simulated): `0.*.css` (Tailwind base + app tokens +
  the fontsource `@font-face` sets) and the trace components' CSS. Fonts are
  `font-display: swap`, so they do not block first paint.
- `unused-javascript`: 24 KB of the 36 KB runtime chunk is unused on `/` — the SvelteKit
  and Svelte runtime; not worth a custom build.
- Nothing else scored below 0.9. Accessibility, best practices and SEO are 100 on both pages
  after the W5 pass (axe-clean at 390 px, OG/Twitter meta and `theme-color` on every
  route, underlined links in running text, 44 px touch targets).

## Cheap wins applied in W5 (web lane)

- `theme-color`, `og:*` / `twitter:*` on every route, canonical links.
- `--fg-dim`, the primary button, the level-chip counts, the severity badges and the
  `severity=` chips were re-tinted for ≥ 4.5:1 (axe `color-contrast`).
- Links inside running text are underlined (axe `link-in-text-block`).
- The curl block's Copy button no longer sits inside a `<summary>` (axe
  `nested-interactive`).

## Not done (deliberately)

- Inlining critical CSS or splitting the trace CSS: not cheap, and the score gap is mostly
  transfer size, which compression fixes.
- Shrinking the prerendered hero payload (the trace is embedded twice: rendered HTML and the
  hydration data, then re-fetched live after hydration): a design change for the trace lane.
