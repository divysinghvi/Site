# Review findings on the Phase 0 drafts (coverage + protocol critics)

Rules for builders: a finding here overrides the draft text it targets. Apply it unless it is factually impossible; then record the deviation in docs/DEVIATIONS.md.

## coverage-01 [blocker] areas: content, repo — Phase 1 cannot run: serve/build/CI all require valid content/ that only exists in Phase 2

**Problem:** Repo §R.5.5: "`serve` prints it and exits 1 before binding the port" on invalid content, and §R.4.1 "Content edits need a restart ... content is loaded once and validated at startup". Phase 1 deliverables (R.10, P.11) include `make dev`, `make promtool-check` (starts `divy-noweb serve`), `make build` (prerenders against the running binary) and the CI `api` job — every one of them needs a `content/` tree that passes `divy validate`, including rule `links.postmortem-bidirectional` (four `gradr.inc-*` spans require four INC files) and `alerts.required`. But BRIEF §6 puts all of `content/` in Phase 2, and no draft says what content exists at the end of Phase 1. As written, Phase 1's own verify command fails.

**Evidence:** draft-repo.md R.10 Phase 1 row lists `validate`, `promtool-check`, `ci.yml api job`; draft-content.md C.10.1 `links.postmortem-bidirectional` and `alerts.required` are errors; BRIEF §6 "Phase 2 — Content. All of `content/`".

**Fix:** Add to the Phase 1 checklist (repo R.10 and content section): "Phase 1 commits the initial content exactly as written in this plan — §C.3.3 spans.yaml, the 11 §C.4.3 log lines, §C.5.2 skeleton for INC-001..004 (same shape, TODO(divy) bodies), §C.6.2 panels.yaml, §C.7 alerts.yaml, §C.8 uptime.yaml, §C.9 manual_metrics.yaml and profile.yaml — so `divy serve` starts; `logs.count` stays a warning until Phase 2 CI runs `--strict`." Phase 2 then completes the 60–100 lines and the four postmortem bodies.

## coverage-02 [blocker] areas: repo, contract, content — The brief's literal curl easter eggs hit Caddy's HTTP→HTTPS redirect and return nothing useful

**Problem:** BRIEF §4 ("all must actually work") and §3.7 give the exact commands `curl divy.dev/metrics`, `curl -H "Accept: text/plain" divy.dev/` and `curl divy.dev/healthz` (returns the JSON). Those are plain-HTTP requests. Repo §R.8.3 Caddyfile is one site block `{$DIVY_DOMAIN} { ... reverse_proxy api:8080 }`, and Caddy's automatic HTTPS answers every port-80 request with a 308 to https://. `curl` does not follow redirects by default, so all three commands print an empty body (or Caddy's redirect page), and the contract's `/contact` copyable curl (K.1.5, C.9.2) is wrong on the live site. The logql draft's Phase 5 note ("Caddy passes `Accept` through unchanged") does not cover this.

**Evidence:** draft-repo.md §R.8.3: "the Caddyfile is the one site block `{$DIVY_DOMAIN} { encode zstd gzip; header {…}; reverse_proxy api:8080 }` — Caddy terminates TLS with automatic certificates"; draft-logql.md Phase 5 row only mentions `Accept` and `X-Divy-Trace-Id`.

**Fix:** Decide now and record in repo §R.8.3 + contract §K.1.5 Notes: add a second Caddy site block `http://{$DIVY_DOMAIN} { @plain path /healthz /readyz /metrics /robots.txt /ascii; handle @plain { reverse_proxy api:8080 } ; @ascii { path / ; header Accept *text/plain* } ; handle @ascii { reverse_proxy api:8080 } ; handle { redir https://{host}{uri} 308 } }` so the brief's exact commands work over HTTP, everything else still redirects; add a Phase 5 checklist line "`curl divy.dev/healthz`, `curl divy.dev/metrics`, `curl -H 'Accept: text/plain' divy.dev/` return bodies without `-L`". Alternative (if HTTP responses for these paths are unwanted): state the assumption and change every printed curl (contact page, robots.txt, footer, README) to `https://divy.dev/...`.

## coverage-03 [blocker] areas: repo — `docker compose up` with the sample .env does not start the stack as specified

**Problem:** BRIEF §2: "Everything must run locally with `docker compose up` and a sample `.env`." Four gaps: (1) the compose file lives at `deploy/docker-compose.yml`, so a bare `docker compose up` in the repo root finds nothing — only `make up` (with `--project-directory . -f deploy/...`) is specified. (2) The `api` service is described as `${DIVY_IMAGE}` = `ghcr.io/divysinghvi/site:latest` with no `build:` block; on a fresh clone (no release yet) compose tries to pull a non-existent image, and `make up ... --build` is a no-op without `build:`. (3) `.env.example` sets `DIVY_DOMAIN=divy.dev`; §R.8.3 claims "Locally ... Caddy serves `localhost` on 80 with an internal certificate", but with that value Caddy attempts ACME for divy.dev and never serves localhost. (4) The runtime image is `distroless/static:nonroot` with `VOLUME /data` and no `/data` directory created/chowned in the image; a fresh named volume is root-owned, `os.MkdirAll`/`open` of `/data/divy.db` fails with EACCES for uid 65532, and the container exits.

**Evidence:** draft-repo.md §R.4 `up`: "`docker compose --project-directory . -f deploy/docker-compose.yml up -d --build`"; §R.8.3: "`api` (`${DIVY_IMAGE}`, `env_file: .env`, ...)" — no `build:`; §R.3.3 `.env.example` `DIVY_DOMAIN=divy.dev`; Dockerfile §R.6.2 `FROM gcr.io/distroless/static-debian13:nonroot ... VOLUME /data` with no mkdir/chown.

**Fix:** Repo §R.1/§R.8.3: add a root `compose.yaml` containing `include: [deploy/docker-compose.yml]` (or move the file to the root) so `docker compose up` works verbatim; give `api` both `image: ${DIVY_IMAGE}` and `build: {context: ., dockerfile: deploy/Dockerfile, args: {SITE_ORIGIN: http://localhost}}`; set `.env.example` `DIVY_DOMAIN=localhost` with a comment "prod: divy.dev" (deploy.sh writes the prod value); in the Dockerfile builder `RUN mkdir -p /out/data` then `COPY --from=api-final --chown=65532:65532 /out/data /data` before `VOLUME /data`. Add the verify line to Phase 5: `cp -n .env.example .env && docker compose up -d && curl -fsS http://localhost/healthz`.

## coverage-04 [major] areas: repo — The floating `promql` console has no query mechanism — only `kubectl get pods` is specified

**Problem:** BRIEF §4: "Typing `promql` anywhere on the site opens a floating query console; `kubectl get pods` in it returns my projects as pods". A query console must execute PromQL. Repo §R.7.2 defines the console only as "open/close, history; `promql` key sequence outside inputs opens it; `kubectl get pods` answered from `profile.pods`" and §R.1 lists `PromqlConsole.svelte (floating), kubectl.ts (pods table from profile)`. Nothing says what any other input does, which endpoint it calls, or how results/errors render.

**Evidence:** draft-repo.md §R.7.2 row "Console"; §R.1 `components/console/`.

**Fix:** Replace the Console row with: "Input dispatch: `kubectl get pods` → table from `/api/content/profile` pods (NAME READY STATUS RESTARTS AGE NOTE, per C.3.7); any other `kubectl …` → `error: the server doesn't have a resource type "<x>"`; `help`/`clear`; everything else → `GET /api/v1/query?query=<input>` rendered as a vector/scalar table (metric labels + value) or the `PromError.error` text in red; ↑/↓ history (sessionStorage), `Esc` closes, `Enter` runs; a `↗ Explore` link opens `/explore?ds=prom&expr=…`."

## coverage-05 [major] areas: repo, content — Type-safety pipeline has no mechanism for union-typed and free-form content fields

**Problem:** Repo §R.5.1 rules: "No extra keys | reflector default (`additionalProperties: false`)" and "Free-form maps | `map[string]string`". But the content schemas need shapes a plain Go struct + invopop defaults cannot produce: `LogLine` has fixed fields plus "any other key: string, number, bool" (C.4.1); `Span.tags` values are "string, number, bool, or list of strings" (`TagValue` anyOf, C.3.2) with a `propertyNames` pattern; `tags.stack`/`lang` are "string or list"; `uptime.targets[].expected_status` is "int or list of int" (C.8); `PromQueryResult.data.result` is a four-way union (vector/matrix/scalar/string) that the contract lists under one Go type. None of the drafts says how these are represented in Go (source of truth), how they survive `yaml.Decoder.KnownFields(true)` (which rejects unknown log keys), what `JSONSchema()`/`JSONSchemaExtend()` hooks emit, or what TypeScript json2ts produces for them. An implementer cannot satisfy §7 "Go structs → JSON schema → TS types generated" for half the content files without inventing this.

**Evidence:** draft-repo.md §R.5.1 struct-tag table; draft-content.md C.4.1 "any other key | string, number, bool", C.3.2 `TagValue` anyOf, C.8 `expected_status | int or list of int`; draft-contract.md K.2.1 `PromQueryResult` "(`data.resultType` ∈ vector, matrix, scalar, string)".

**Fix:** Add to §R.5.1 a table "Non-struct shapes" naming each field and its mechanism: `LogLine{…fixed…; Extra map[string]Scalar `json:"-"`}` with custom (Un)MarshalJSON and `JSONSchemaExtend` setting `additionalProperties: {"$ref":"#/$defs/Scalar"}` + `propertyNames.pattern` (validation of NDJSON bypasses `KnownFields`); `type StringOrList []string` with `UnmarshalYAML/JSON` accepting scalar or list and `JSONSchema()` returning `anyOf`; `type TagValue struct{…}` likewise; `type IntOrList []int` for `expected_status`; `PromResult json.RawMessage` with `JSONSchema()` returning `oneOf` of the four result shapes so TS gets a discriminated union on `resultType`. State that json2ts emits `unknown`/index signatures for these and that `prom.ts`/`loki.ts` narrow them in one place each (extending the existing `toSamplePair` rule).

## coverage-06 [major] areas: promql, repo, logql — Several commands are not single-paste runnable

**Problem:** (1) PromQL §P.9 CI one-liner ends with `kill "$(cat /tmp/divy.pid)"; exit $rc` — pasted into an interactive shell, `exit` closes the user's terminal; it also starts with `cd api` (changes the caller's cwd) and uses `until … ping …; do sleep 1; done` with no bound, so a binary that fails to start hangs forever. (2) Repo §R.4 `promtool-check` recipe references `$$TMP/promtool.db` but nothing defines `TMP`. (3) LogQL §L.5.6 verify is two commands with a `<id>` placeholder ("then `curl -s https://divy.dev/api/traces/<id> | jq …`"). (4) Repo §R.4.1 step 4 `open http://localhost:5173/` is macOS-only.

**Evidence:** draft-promql.md §P.9: "… rc=$?; kill "$(cat /tmp/divy.pid)"; exit $rc"; draft-repo.md §R.4 `promtool-check`: "`bin/divy-noweb serve --addr 127.0.0.1:18080 --db $$TMP/promtool.db & `"; draft-logql.md §L.5.6 "Verify" row; BRIEF: "Every command you give me must be runnable as a single paste."

**Fix:** §P.9: wrap in a subshell and bound the wait: `( cd api && go build -tags noweb -o ../bin/divy-noweb ./cmd/divy && ../bin/divy-noweb serve --addr 127.0.0.1:18080 --db "$(mktemp -d)/promtool.db" --content ../content & pid=$!; for i in $(seq 1 30); do ../bin/divy-noweb ping --url http://127.0.0.1:18080/readyz && break; sleep 1; done; curl -sf http://127.0.0.1:18080/metrics | go run github.com/prometheus/prometheus/cmd/promtool@v0.314.0 check metrics --extended; rc=$?; kill $pid; exit $rc )` — and say the canonical form is `make promtool-check`. §R.4: define `TMP := $(shell mktemp -d)` in the recipe and the same 30-try loop. §L.5.6: `id=$(curl -sI https://divy.dev/healthz | tr -d '\r' | awk 'tolower($1)=="x-divy-trace-id:"{print $2}'); curl -s "https://divy.dev/api/traces/$id" | jq '.data[0].spans[].operationName'`. §R.4.1: replace `open …` with "browse to http://localhost:5173/".

## coverage-07 [minor] areas: repo — `make lint-api` runs golangci-lint without the `noweb` tag and fails on the missing embed directory

**Problem:** §R.6.2: "Building without the tag and without `dist/` fails at compile time (embed patterns must match)". §R.4 `lint-api` = "`go vet -tags noweb ./... && golangci-lint run`" — the second command type-checks `web.go` (`//go:embed all:dist`) without the tag, so on a checkout without `web/build` (every PR `api` job) lint fails.

**Evidence:** draft-repo.md §R.4 `lint-api` row; §R.6.2 `web.go`.

**Fix:** `lint-api`: `golangci-lint run --build-tags noweb` (and set `run.build-tags: [noweb]` in `api/.golangci.yml`).

## coverage-08 [minor] areas: logql, contract — `/api/services` example omits `freelance` and states total 9

**Problem:** LogQL §L.4.1 defines services as "every `spans.yaml` service owning ≥ 1 span (`community` has none → excluded) plus `divy-api`". `freelance.web-dev` is a span with `service: freelance`, so the list is divy, edu, freelance, ef-polymer, euro-tech, gradr, oss, project, quant + divy-api = 10. Both the logql example and contract K.2.3 print 9 names without `freelance`.

**Evidence:** draft-logql.md §L.4.1: `{"data":["divy","divy-api","edu","ef-polymer","euro-tech","gradr","oss","project","quant"],"total":9,…}`; draft-contract.md K.2.3 same; draft-content.md C.3.3 `freelance.web-dev` `service: freelance`.

**Fix:** Insert `"freelance"` after `"euro-tech"`... i.e. sorted: `["divy","divy-api","edu","ef-polymer","euro-tech","freelance","gradr","oss","project","quant"]`, `"total":10` in both files; L.7.4 row 15 assertion unchanged.

## coverage-09 [minor] areas: content, repo — Alert `runbook_url` deep links use a URL form no route defines

**Problem:** C.7 alerts carry `runbook_url: /#dashboard?panel=commits-weekly` and `/#dashboard?panel=lfx-pending`; Content X7 calls them placeholders "the frontend section owns". Repo R5/§R.7.2 define only `/dashboard#range=…&layout=…` and `/#trace?span=…`; `hash.ts` has no `panel=` key, and `/#dashboard…` on `/` is meaningless. The alert toast's runbook link therefore lands nowhere.

**Evidence:** draft-content.md C.7 `runbook_url: /#dashboard?panel=commits-weekly`; draft-repo.md R5 "URL state adopts … `/dashboard#range=7d&layout=…`, span deep link `/#trace?span=<span id>`" (no panel key).

**Fix:** Decide now: `runbook_url: /dashboard#panel=commits-weekly`; add `panel=<id>` to `hash.ts` (scroll the panel into view and flash its header); Phase 2 writes the final value; drop X7.

## coverage-10 [minor] areas: content — Brief's "outage resolved" span event has no place in the tree

**Problem:** BRIEF §3.1: "span events (promotion, first prod deploy, outage resolved)". C.3.3 has `promoted to Product Engineer` and two `first prod deploy` events, but the four `gradr.inc-*` spans carry no events at all, so no "outage resolved" marker exists.

**Evidence:** draft-content.md C.3.3 `gradr.inc-001`…`inc-004` blocks: `status: error`, `links`, no `events`.

**Fix:** Add to each incident span: `events: [{ts: TODO(divy), name: outage resolved, attrs: {incident: INC-00N}}]` (the resolved-at TODO also feeds the postmortem timeline).

## coverage-11 [minor] areas: repo — No UI to paste an `X-Divy-Trace-Id` into the trace viewer

**Problem:** BRIEF §4: "paste it into the trace viewer and it resolves to the request's own span". The plan provides `/trace/[id]` (route) and says "paste into `/trace/<id>` (UI)" (L.5.6), i.e. the user edits the URL. No component accepts a pasted id.

**Evidence:** draft-repo.md §R.7.1 `/trace/[id]` row; draft-logql.md §L.5.6 "paste into `/trace/<id>` (UI)".

**Fix:** Add to §R.7.1 `/` and `/trace/[id]` rows: a Jaeger-style "Trace ID" input in the hero header (`components/trace/TraceIdBox.svelte`): on submit navigates to `/trace/<id>`; validates `career` or 32 hex; on 404 shows the `X-Divy-Trace-Sampled: 0` explanation from the error body.

## coverage-12 [minor] areas: repo — Logs explorer autocomplete, level chips and the Explore page are named but not specified

**Problem:** BRIEF §3.3 requires "query bar with LogQL autocomplete, level filter chips"; §5 requires an "Explore" affordance on every panel. Repo lists `QueryBar.svelte (LogQL autocomplete)`, `LevelChips.svelte`, `lib/api/loki.ts … autocomplete sources`, and `/explore` as "yes (empty shell)" with a list of endpoints. Unspecified: what the completion list contains and where each item comes from; how a chip changes the query (rewrite the selector? add `| level="warn"`? multi-select semantics); what `/explore` renders.

**Evidence:** draft-repo.md §R.1 `logs/` components; §R.7.1 `/explore` "yes (empty shell)".

**Fix:** Add a short table to §R.7.2: Autocomplete = label names from `/loki/api/v1/labels`, values from `/label/{name}/values` when the caret is inside `{…}`, stages `| json`, `|=`, `!=`, `|~`, `!~`, and the subset's functions from a static list mirroring `docs/logql-subset.md`. Level chips = multi-select toggles that rewrite the `level` matcher of the first selector to `level=~"warn|error"` (all selected = matcher removed). Explore = data-source tabs (prom/loki), query bar with the same autocomplete, range picker, Run, result as timeseries (uPlot) or table/streams, and a curl line — the same `QueryInspector` component.

## coverage-13 [minor] areas: content, logql — Invented specifics about Divy that are neither in the brief nor marked TODO(divy)

**Problem:** (1) INC-001 skeleton Resolution: "Startup ordering (app containers depend on the sidecar's healthcheck) and a healthcheck that only passes once `.env` exists and is non-empty" and action item "Add `depends_on` with `condition: service_healthy` … — done": the brief says only "Fixed with startup ordering + healthcheck gating". (2) Severity assignments SEV1/SEV2/SEV3 for the four incidents are editorial, not given. (3) `oss.minikube.pr-0N` stubs carry `lang: [go]` although each PR's nature is a TODO. (4) Sample log line `"containers":65` states the brief's "~65" as exact. (5) `project.savely` `lang: [python]` although the stack includes Svelte. (6) The tagline "a career, traced" (og default in Go, `<title>` on `/`) is prose about Divy living outside `content/`.

**Evidence:** draft-content.md C.5.2 skeleton; C.5.1 severity table "Assigned"; C.3.3 `oss.minikube.pr-01 … tags: {repo: kubernetes/minikube, lang: [go]}`; C.4.3 `"containers":65`; draft-logql.md §L.6.7 "`a career, traced` (Inter 36 px)"; draft-repo.md §R.7.1 title `divy.career — a career, traced`.

**Fix:** (1) Rewrite as "Startup ordering and healthcheck gating — TODO(divy): exact mechanism (compose `depends_on`/healthcheck? systemd ordering?)" and mark the action item `TODO(divy)`. (2) Keep the definitions, set frontmatter `severity` per table but add `# TODO(divy): confirm severity` comments. (3) PR stubs: `lang: TODO(divy)`. (4) Use `"containers_approx":65` or `"containers":"~65"`. (5) `lang: [python, javascript]` or `TODO(divy)`. (6) Add `tagline` to `profile.yaml` (`tagline: TODO(divy): one-line tagline`), served via `/api/content/profile`, used by og/default.png and titles.

## coverage-14 [minor] areas: content — `hide: true` target semantics undefined; `stars-by-repo` expr disagrees with the storage note

**Problem:** C.6.1 lists `hide` (bool) with no meaning; rule `panels.manual-source` requires the updated_metric to be "referenced by a hidden target", and the stat panels rely on series B being *executed* — the opposite of Grafana's `hide` (which skips the query). Also Storage §S.4.1 says "0-star repos included — panels filter with `github_stars > 0`" but the `stars-by-repo` target is `github_stars` unfiltered, so every zero-star repo shows as a bar.

**Evidence:** draft-content.md C.6.1 `hide` (bool); C.6.2 `stars-by-repo` `expr: github_stars`; draft-storage.md §S.4.1 "panels filter with `github_stars > 0`".

**Fix:** C.6.1: "`hide: true` = the query runs but is not drawn; its latest value is available to the panel (used for last-updated stamps). This intentionally differs from Grafana's hide." Change `stars-by-repo` to `expr: github_stars > 0` (or drop the sentence in §S.4.1).

## coverage-15 [minor] areas: content — Sanitizer rule 4 "phone-number shapes" has no regex

**Problem:** C.5.3 row 4 is an error-level rule (`pm.sanitize`, error) but the phone pattern is described only as "phone-number shapes", so Phase 1 must invent it and the false-positive surface (port `2465`, durations, PR numbers) is undefined.

**Evidence:** draft-content.md C.5.3 row 4: "email addresses `[\w.+-]+@[\w-]+\.[\w.]+` (except `TODO(divy)`), phone-number shapes".

**Fix:** Specify: `(?:\+\d{1,3}[ -]?)?(?:\(?\d{2,4}\)?[ -]?)\d{3,4}[ -]?\d{4}\b` requiring ≥10 digits total, ignoring matches inside code fences and the string `2465`; level warn (not error) because of false positives.

## coverage-16 [minor] areas: storage, content, repo — Self-API uptime probe cannot show red for the failures that matter, and the plan does not say so

**Problem:** BRIEF §3.4: probe "this site's own API … If something is down, it shows red. Do not fake green." Repo §R.8.3 sets production `UPTIME_SELF_URL=http://api:8080/readyz` (container-internal), so Caddy/TLS/DNS failures are invisible to the probe, and because the prober is the same process, a full outage produces a gap (grey buckets), never red. Neither limitation is stated on the uptime page or in `uptime.yaml`.

**Evidence:** draft-repo.md §R.8.3 "`UPTIME_SELF_URL=http://api:8080/readyz`"; draft-content.md C.8 `self-api` target; draft-storage.md §S.4.3 has no self-probe caveat.

**Fix:** Production compose: `UPTIME_SELF_URL=https://${DIVY_DOMAIN}/readyz` (hairpin through Caddy; fall back to the internal URL only if the host cannot resolve itself — state which in Phase 5). Add `note: "probed from inside the same process: a full outage shows as a gap, not red"` to the `self-api` target and render `note` on the uptime page.

## coverage-17 [minor] areas: logql, repo — Static `favicon.ico` "fixed sparkline glyph" is a static sparkline

**Problem:** BRIEF §2 opening: "nothing is a static image or fake sparkline"; §4: the favicon "is a tiny live sparkline". L.6.3 adds `web/static/favicon.ico` = "a fixed sparkline glyph, committed … it is not live". A committed sparkline-shaped icon is exactly a fake sparkline for every client that prefers `.ico`.

**Evidence:** draft-logql.md §L.6.3 `/favicon.ico` row; L-X7.

**Fix:** Either drop `/favicon.ico` (a 404 is harmless; the `<link rel="icon" type="image/svg+xml">` is what modern browsers use) or make the `.ico` a non-chart glyph (a green dot on the near-black background) so nothing static resembles data.

## coverage-18 [minor] areas: repo — `TRUSTED_PROXIES` = "the compose network CIDR" is not a known value

**Problem:** §R.3.2 and §R.8.3 set `TRUSTED_PROXIES` to "the compose network CIDR", but compose allocates the default network subnet dynamically per host, so the value cannot be written into `.env`/compose without pinning the subnet. With an empty/wrong value every visitor is keyed by Caddy's IP and one bucket rate-limits the whole site.

**Evidence:** draft-repo.md §R.3.2 `TRUSTED_PROXIES | empty (compose: the compose network CIDR)`; §R.8.3 "`TRUSTED_PROXIES` = the compose network CIDR".

**Fix:** In `deploy/docker-compose.yml` declare `networks: default: ipam: config: [{subnet: 172.28.0.0/24}]` and set `TRUSTED_PROXIES=172.28.0.0/24` in the compose `environment:` (and `.env.example` comment).

## coverage-19 [minor] areas: repo, storage, logql, content — Open questions and phase checklists are scattered; two 'questions' are assumptions

**Problem:** Total open questions = 3 (frontend delivery, inline in repo §R.6; storage Q1; logql Q1) — within the limit, but the assembled PLAN.md needs one list and the CONVENTIONS ask to "prefer stating an assumption over asking". Storage Q1 already ends "The plan assumes the classic `repo` token; nothing else changes if the answer is public-only" — that is an assumption. LogQL Q1 asks whether to keep metric queries in Phase 1; it too can be an assumption. Phase checklists: repo R.10, promql P.11 and logql have per-phase tables; storage and content have none, and no phase has a single paste-able verify command (BRIEF §6 asks for one at every checkpoint).

**Evidence:** draft-storage.md "## Open questions 1. GitHub token type …"; draft-logql.md "## Open questions 1. …"; draft-repo.md §R.6 "(recommended; open question #1)"; no `## Phase` table in draft-storage.md or draft-content.md.

**Fix:** PLAN.md gets one "Open questions" section with exactly: (1) adapter-static+embed vs adapter-node. Move storage Q1 to its "Assumptions to verify at Phase 1" list; move logql Q1 to an "Assumptions" line ("metric queries ship in Phase 1; say 'no' to defer"). Add "What each phase owes" tables to storage (Phase 1: store, migrations, collectors, catalogue, cache, rate limit, tests) and content (Phase 1: initial files per coverage-01; Phase 2: full tree/logs/postmortems). Add a verify command per phase in the consolidated checklist: P1 `make test && make validate && make promtool-check`; P2 `make validate` (`--strict`) `&& make todos`; P3 `make build && make e2e`; P4 `make e2e && make lighthouse`; P5 `make docker && make up && curl -fsS http://localhost/readyz`.

## coverage-20 [minor] areas: promql, logql — PromQL/LogQL scope is widened well beyond the brief without saying which additions are required

**Problem:** BRIEF §2 asks for "instant vector selectors, label matchers, `rate()`, `sum()`, `increase()`, `[range]`" and Loki "stream selectors, line filters `|=`, `!=`, `|~`, and `| json`". PromQL §P.1 adds `avg min max count`, `irate delta *_over_time`, `abs ceil floor round clamp_*`, `time vector scalar`, full arithmetic/comparison operators and six extra endpoints; LogQL adds `!~`, label filters (string/numeric/duration, and/or), `count_over_time`, `rate`, five aggregations, `vector()` arithmetic, and `/series`, `/index/stats`, `/index/volume`, `/status/buildinfo`. Some of this is mandatory for the brief's Grafana requirement (comparisons for the alert exprs, `1+1`/`vector(1)+vector(1)` health checks, `label/__name__/values`, `metadata`, `buildinfo`); the rest is discretionary. The plan never separates the two, so the user cannot see what the brief's "subset" grew into or trim it.

**Evidence:** draft-promql.md §P.1 table; draft-logql.md §L.1.2 grammar and §L.2.2 endpoint table; BRIEF §2 bullets.

**Fix:** Add a one-table "Why beyond the brief" to each section with columns Construct | Needed by (Grafana health check / alert exprs / panel exprs / Explore volume histogram / none — cheap) so the widening is explicit; keep the implementation as planned.

## coverage-21 [minor] areas: repo, content — Hero trace is a build-time snapshot: open-span durations freeze until the next deploy

**Problem:** §R.7.1 `/` row: live data "none for the trace (static per deploy)". `/api/traces/career` computes `duration` for open spans per request (C.3.5 "`now` evaluated per request"), but the prerendered page bakes the build-time value; C.3.6 tells the frontend to "extend to the right edge" for `divy.open=true`, which fixes the bar but not the drawer's printed duration, the ASCII-vs-page mismatch, or the §R.6.1 "as of <build time>" label (which applies to panels only). BRIEF §2: "Every panel must be backed by a real API … nothing is a static image".

**Evidence:** draft-repo.md §R.7.1 `/` row "none for the trace (static per deploy)"; draft-content.md C.3.5 duration row "`now` evaluated per request".

**Fix:** §R.7.1 `/` live column: "re-fetch `/api/traces/career` once after hydration (Q15-cached, ~20 KB) and replace the snapshot; until then the drawer computes open-span durations from `Date.now()` using `divy.open`/`divy.start`".

## protocol-01 [major] areas: logql, contract — Loki integer timestamps with ≤10 digits are SECONDS, not nanoseconds

**Problem:** draft-logql.md §L.2.1 says: "`start`, `end`, `time` | contains `.` → float seconds; all digits → **nanoseconds** since epoch; else RFC 3339". draft-contract.md §K.3.6 repeats: "Loki | `start`, `end`, `time` | contains `.` → float seconds; all digits → **nanoseconds**". Real Loki (`pkg/loghttp/params.go` `parseTimestamp`, lines 187-197) does `nanos, err := strconv.ParseInt(value, 10, 64)` and then `if len(value) <= 10 { return time.Unix(nanos, 0), nil }` before `return time.Unix(0, nanos), nil`. So `start=1757030400` (10 digits) is 2026-09-05 in Loki but 1.757 s after the epoch under the draft's rule; the draft's own cross-family canonicalisation (§K.3.2 Q15 rewrites `start`/`end` to ms) would silently produce an empty window for any human/logcli-style seconds input. Grafana is unaffected (it sends 19-digit `UnixNano()` strings, grafana-loki-datasource `pkg/loki/api.go` lines 68-69, 85), but the plan claims Loki-exact parsing and the phase-1 parser table would be wrong.

**Evidence:** https://raw.githubusercontent.com/grafana/loki/main/pkg/loghttp/params.go (parseTimestamp, lines 175-198); https://raw.githubusercontent.com/grafana/grafana-loki-datasource/main/pkg/loki/api.go (lines 68-69, 85: `strconv.FormatInt(query.Start.UnixNano(), 10)`)

**Fix:** Replace the rule in §L.2.1 and §K.3.6 with: "contains `.` → float seconds (fraction rounded to ms); integer with ≤ 10 digits → Unix **seconds**; integer with > 10 digits → Unix **nanoseconds**; else RFC 3339 / RFC 3339-nano (Loki `parseTimestamp`)". Add test rows to §L.7.4: `start=1757030400` ≡ `start=1757030400000000000` ≡ `start=2026-09-05T00:00:00Z`; `start=4000000000` (Grafana health-check `time`) = 10 digits → seconds. Update facts-logql L12 accordingly.

## protocol-02 [major] areas: contract, promql, storage — 2h default lookback delta empties every sub-day range query over the daily-only counter history

**Problem:** draft-contract.md K-I1 resolves the lookback conflict to 2h with the rationale: "Storage's own §S.2.3 rule 2 (live sample at every run) and §S.2.4 (1 h heartbeat) guarantee a sample within 2 h for every healthy series". That is only true at `now`. draft-storage.md §S.2.3 stores counters as "One sample at `dayEnd(d)` for every day" plus "Exactly one off-grid sample at `now` … the previous live sample(s) of the series are deleted (`DeleteOffGrid`)", so historical counter series (`github_commits_total`, `github_contributions_total`, `github_merged_prs_total{org}`, `github_merged_prs_by_repo_total`, `pypi_downloads_total`) have exactly one sample per 24 h. Prometheus lookback semantics (basics.md "Staleness": an instant selector takes "the newest sample that is less than the lookback period ago") mean a raw selector evaluated at any step more than 2 h after 00:00Z returns nothing. Consequences: panel `merged-prs-by-org` (`expr: github_merged_prs_total`, draft-content §C.6.2) at the site's own steps (24h→5m, 7d→1h, 30d→6h per draft-promql §P.11 row 3) has 22/24 of its points missing; in Grafana a 1y range gets `step = RoundInterval(365d/1500) = 6h` (facts-promql "Step"), so 3 of every 4 points are empty and a 7d range (`step=5m`) shows 24 points per day then a gap. `rate()`/`increase()` panels are unaffected (range vectors ignore lookback), but every raw gauge/counter timeseries panel and any Grafana user's `github_merged_prs_total` query breaks. Storage S-X2's 26h ("Stored series are daily-gridded … so the PromQL engine's default lookback delta must be **26h**") was the correct mechanism.

**Evidence:** https://raw.githubusercontent.com/prometheus/prometheus/main/docs/querying/basics.md ("Staleness" — newest sample less than the lookback period ago; default 5m); https://raw.githubusercontent.com/grafana/grafana/v12.4.0/pkg/promlib/models/query.go (step = max(minInterval, RoundInterval((to−from)/maxDataPoints))); draft-storage.md §S.2.3 rules 1-2 (daily grid + single live sample, DeleteOffGrid)

**Fix:** In draft-contract.md K-I1: resolve to **`QUERY_LOOKBACK_DELTA` default `26h`** (storage S-X2), keep the per-request `lookback_delta` override, and replace the rationale with: "counter history is one sample per UTC day (§S.2.3), so any lookback shorter than 24h + collector cadence leaves raw selectors empty between day boundaries; freshness of live data is signalled by the `/metrics` staleness cut-off (K-I2) and `divy_collector_last_success_timestamp_seconds`, not by the query lookback." In draft-promql.md P3 change `2h` → `26h` everywhere (P3, §P.5.1 lookback row, §P.6, §P.10.3 server-under-test, §P.11 env list, E2 expectation "@6d+5m (26h)"). Add to §P.8 compatibility notes: "Grafana never sends `lookback_delta`; with the 26h default a raw counter selector renders as a daily step function at any Grafana step." Alternative if 2h is kept for honesty: every raw counter panel expr must become `last_over_time(<metric>[1d])` and §P.8 must warn Grafana users to set Min step = 1d — say so explicitly.

## protocol-03 [minor] areas: promql, contract — Prometheus 3.x accepts `/api/v1/label/1bad/values` (UTF-8 name validation); the 400 test row is wrong

**Problem:** draft-promql.md §P.7.2: "`{name}` must match `[a-zA-Z_][a-zA-Z0-9_]*` (else 400 `invalid label name: \"1bad\"`)" and §P.10.3 H13: "`GET /api/v1/label/1bad/values` … 400 `invalid label name: \"1bad\"`"; draft-contract.md §K.1.1 repeats "400 `invalid label name: \"1bad\"`". Prometheus v0.314.0 `web/api/v1/api.go` line 903 validates with `if !model.UTF8Validation.IsValidLabelName(name)`; under UTF-8 validation any non-empty valid-UTF-8 string is a legal label name, so `1bad` returns 200 `{"status":"success","data":[]}`. The error text `invalid label name: %q` is correct but only reachable with an empty or non-UTF-8 name.

**Evidence:** https://raw.githubusercontent.com/prometheus/prometheus/v0.314.0/web/api/v1/api.go (lines 895-904, `labelValues`); https://raw.githubusercontent.com/prometheus/common/main/model/labels.go (`ValidationScheme.IsValidLabelName`); https://raw.githubusercontent.com/prometheus/common/main/model/metric.go (line 51 `NameValidationScheme = UTF8Validation`)

**Fix:** §P.7.2 and §K.1.1: "`{name}` must be non-empty valid UTF-8 (Prometheus 3 UTF-8 name validation); else 400 `invalid label name: \"…\"`; a legal-but-unknown name returns `data: []`". H13: change the case to `GET /api/v1/label/1bad/values` → 200 `{"status":"success","data":[]}` and add a genuinely invalid name (percent-encoded invalid UTF-8, e.g. `%FF`) → 400.

## protocol-04 [minor] areas: logql — Loki `| json` collision rule now verified: Loki always suffixes `_extracted`, regardless of value equality

**Problem:** facts-logql.md lists as UNVERIFIED: "Loki's rule that an extracted label colliding with a stream label is renamed `<key>_extracted`". Verified: docs say "If an extracted label key name already exists in the original log stream, the extracted label key will be suffixed with the `_extracted` keyword", and `pkg/logql/log/parser.go` lines 152-153 do `if j.lbs.BaseHas(sanitizedKey) || j.lbs.HasInCategory(sanitizedKey, StructuredMetadataLabel) { sanitizedKey = sanitizedKey + duplicateSuffix }` with `duplicateSuffix = "_extracted"` — no value comparison. draft-logql.md §L.1.6 ("identical value → skipped (no duplicate); different value → emitted as `<key>_extracted`") and test row §L.7.2 #8 ("no `*_extracted` keys") are therefore a deliberate divergence, correctly flagged in L-X3, but §L.1.5 says grouping happens "exactly as Loki does" and `docs/logql-subset.md` (= §L.1–L.2 verbatim) would ship the divergence without saying what Loki does.

**Evidence:** https://raw.githubusercontent.com/grafana/loki/main/docs/sources/query/log_queries/_index.md (line 294); https://raw.githubusercontent.com/grafana/loki/main/pkg/logql/log/parser.go (lines 25, 152-153, 182-185)

**Fix:** Move the fact to the verified table in facts-logql.md. In §L.1.6 "Collision with a stream label" row write: "**Divergence from Loki** (which always renames the extracted key to `<key>_extracted` when the stream already has `<key>`, even for equal values): identical value → skipped; different value → `<key>_extracted`. Reason: stream labels are copied from the line (§C.4.2), so Loki's rule would add `service_extracted`/`level_extracted` to every parsed line." Keep row #8; add a row asserting `service_extracted` appears when the values differ (the synthetic line in row #14 already does).

## protocol-05 [minor] areas: logql, contract — Grafana's Loki backend sends `step=<n>ms`, `time`/`start`/`end` as UnixNano, and `limit` only for log queries — add to the parser rules and the Grafana flow table

**Problem:** draft-logql.md §L.2.5 lists the range request as "`…&limit=1000&direction=backward&step=…`" and the health check as "instant query `vector(1)+vector(1)` (`step=1s`) → `/loki/api/v1/query`". Verified grafana-loki-datasource `pkg/loki/api.go`: range → `qs.Set("step", fmt.Sprintf("%dms", query.Step.Milliseconds()))` (e.g. `step=15000ms`; the code comment says Loki "does not support step with float number and time-specifier"), `start`/`end` = `UnixNano()` decimal strings, `limit` sent only when `MaxLines > 0` ("Loki does not like limit=0"); instant → only `query`, `direction`, `time=<UnixNano>` (no `step`, so the health check request is `GET /loki/api/v1/query?direction=backward&query=vector(1)%2Bvector(1)&time=4000000000`). The health check then requires exactly 1 frame with 2 fields, 1 row, value `2` (healthcheck.go lines 56-77). `step=15000ms` is legal only if the implementation parses with Prometheus `model.ParseDuration` (accepts `ms`), not Go `time.ParseDuration` semantics restricted to the draft's `NUMBER unit` lexer table (§L.1.1 lists `ms` — fine — but §L.2.1 must say which parser).

**Evidence:** https://raw.githubusercontent.com/grafana/grafana-loki-datasource/main/pkg/loki/api.go (lines 48-94); https://raw.githubusercontent.com/grafana/grafana-loki-datasource/main/pkg/loki/healthcheck.go (lines 25-77)

**Fix:** §L.2.1 `step` row: "float seconds or Prometheus duration (`model.ParseDuration`; Grafana sends integer milliseconds with unit, e.g. `step=15000ms`)". §L.2.5 row 1: "`GET /loki/api/v1/query?direction=backward&query=vector(1)%2Bvector(1)&time=4000000000` (10 digits ⇒ seconds, protocol-01); no `step`, no `limit`; Grafana requires one frame with two fields and the single value 2". Row 2: `…&start=<ns>&end=<ns>&step=<ms>ms&limit=1000&direction=backward`; note `limit` is absent for metric queries. Add HTTP test rows for `step=15000ms` and for a request without `limit`.

## protocol-06 [minor] areas: logql, contract — Loki's `limit ≤ 0` error text is `limit must be a positive value`

**Problem:** draft-logql.md §L.2.1 and draft-contract.md §K.3.7 state "`< 1` → 400 `limit must be between 1 and 5000`". Loki `pkg/loghttp/params.go` `limit()` (lines 27-38) returns `errors.New("limit must be a positive value")` for `l <= 0` and `limit value %d is out of range [0, %d]` above MaxUint32; the 5000 cap is a separate per-tenant limit (`ErrMaxEntriesLimit = "max entries limit per query exceeded"`, `pkg/logqlmodel/error.go` line 26) applied later. Since the plan claims Loki-verbatim error strings elsewhere, keep this one verbatim too.

**Evidence:** https://raw.githubusercontent.com/grafana/loki/main/pkg/loghttp/params.go (lines 27-38); https://raw.githubusercontent.com/grafana/loki/main/pkg/logqlmodel/error.go (line 26)

**Fix:** Replace `limit must be between 1 and 5000` with `limit must be a positive value` in §L.2.1, §L.2.4 and §K.3.7; keep the `max entries limit per query exceeded, limit > max_entries_limit_per_query (6000 > 5000)` text for values above 5000.

## protocol-07 [minor] areas: repo — json2ts ignores `prefixItems` (emits `unknown[]`); use `items` + `minItems`/`maxItems` to get a real tuple type

**Problem:** draft-repo.md §R.5.3: "Prometheus sample pairs … are declared in Go with a custom `JSONSchema()` returning `{\"type\":\"array\",\"prefixItems\":[{\"type\":\"number\"},{\"type\":\"string\"}],\"minItems\":2,\"maxItems\":2}`; if json2ts emits `unknown[]` for it, `prom.ts` narrows…". The json-schema-to-typescript README compatibility table states for 2020-12: "`prefixItems` | ignored | `unknown[]`; … Tuples are #816 (pending)" and lists `prefixItems` under "Not supported from 2019-09 / 2020-12" (the feature checklist line claiming tuple support contradicts the table; the table is the authoritative per-keyword statement). So the `unknown[]` branch is the certain outcome, and every consumer of `PromQueryResult` loses type safety on the one field that matters.

**Evidence:** https://raw.githubusercontent.com/bcherny/json-schema-to-typescript/master/README.md (compatibility table rows `prefixItems` and "Not supported from 2019-09 / 2020-12"; options `maxItems`/`ignoreMinAndMaxItems` describe bounded-array tuple emission)

**Fix:** Declare the pair as `{"type":"array","items":{"type":["number","string"]},"minItems":2,"maxItems":2}` — valid 2020-12 for santhosh-tekuri and turned by json2ts's bounded-array rule into the tuple `[number | string, number | string]`; keep `toSamplePair` as the single narrowing point (`[number, string]`). Remove the `prefixItems` example from §R.5.3 and state that `prefixItems` is unsupported by json2ts 16.

## protocol-08 [minor] areas: logql — `resource.Merge` error is discarded; the resource silently loses its schema URL on any semconv drift

**Problem:** draft-logql.md §L.5.1: `res, _ := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL, …))` with `semconv = go.opentelemetry.io/otel/semconv/v1.43.0`. Verified: sdk v1.46.0 `sdk/resource/builtin.go` line 16 imports `semconv/v1.43.0`, so today the two schema URLs match and `Merge` succeeds. But `sdk/resource/resource.go` lines 218-229: when both schema URLs are non-empty and differ, `Merge` returns `NewSchemaless(combine...)` plus `ErrSchemaURLConflict`. The blank-identifier discard means a Dependabot bump of either module (the repo plan enables weekly gomod updates) silently drops the schema URL; nothing fails, but the plan's own pin comment ("the version otelhttp v0.71.0 emits") becomes untrue without a signal.

**Evidence:** https://raw.githubusercontent.com/open-telemetry/opentelemetry-go/main/sdk/resource/resource.go (lines 199-229, `Merge`); https://raw.githubusercontent.com/open-telemetry/opentelemetry-go/sdk/v1.46.0/sdk/resource/builtin.go (line 16)

**Fix:** Replace the snippet with `res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL, …)); if err != nil { log.Fatalf("otel resource: %v (semconv version differs from the SDK's; align the pins)", err) }` and add to §L.5.1 pin note: "`semconv` must be the version `sdk/resource` itself uses (v1.43.0 in sdk v1.46.0); `Merge` errors with `ErrSchemaURLConflict` otherwise".

## protocol-09 [minor] areas: storage — `commitContributionsByRepository(maxRepositories: 100)` — 100 is the API's cap (community-sourced, not in the schema); state it as the maximum, not an arbitrary choice

**Problem:** facts-storage.md UNVERIFIED: "The maximum accepted value of `commitContributionsByRepository(maxRepositories:)` (schema gives only the default 25). The plan uses 100". The octokit schema mirror only documents `maxRepositories: Int = 25` (schema.graphql line 6265). Official GitHub docs do not state a maximum; multiple GitHub Community discussions report that values above 100 are rejected and that `maxRepositories: 100` is the ceiling, with `totalRepositoriesWithContributedCommits` the only way to detect truncation. So draft-storage.md §S.4.1 Q1 is at the cap already and the truncation guard ("`> 100` repositories … warning `commit series may be incomplete`") is the right mechanism — but the plan should not imply a larger value is available.

**Evidence:** https://raw.githubusercontent.com/octokit/graphql-schema/master/schema.graphql (line 6261-6265); https://github.com/orgs/community/discussions/112637 ; https://github.com/orgs/community/discussions/24350

**Fix:** In §S.4.1 Q1 and the "Assumptions to verify at Phase 1" list write: "`maxRepositories: 100` is the API maximum (undocumented in the schema; reported by GitHub Community, verify with one query at Phase 1); if `totalRepositoriesWithContributedCommits > 100` in a window, split that window into shorter date ranges instead of raising `maxRepositories`."

## protocol-10 [minor] areas: promql — Grafana never POSTs `/api/v1/rules`; §P.8 row 2 contradicts constants.ts and the contract's own K-I6 resolution

**Problem:** draft-promql.md §P.8 row "Data source load": "`POST /api/v1/rules` (form body, empty) — on 405/400 retried as `GET`". Verified Grafana v12.4.0 `packages/grafana-prometheus/src/constants.ts` lines 37-43: `GET_AND_POST_METADATA_ENDPOINTS = ['api/v1/query','api/v1/query_range','api/v1/series','api/v1/labels','suggestions']`; `/api/v1/rules` is not listed, so `metadataRequest('/api/v1/rules', {})` (datasource.ts line 168) is always a GET. draft-contract.md K-I6 already decides GET-only but leaves the promql text to be fixed.

**Evidence:** https://raw.githubusercontent.com/grafana/grafana/v12.4.0/packages/grafana-prometheus/src/constants.ts (lines 37-43); https://raw.githubusercontent.com/grafana/grafana/v12.4.0/packages/grafana-prometheus/src/datasource.ts (lines 164-174, 189-197)

**Fix:** §P.8 row 2: "`GET /api/v1/rules` (errors ignored — 'Rules API is experimental'); `GET /api/v1/query_exemplars?query=test&start=<ms>&end=<ms>`". Drop the 405/400 retry sentence (it applies only to `query`, `query_range`, `series`, `labels`).

## protocol-11 [minor] areas: logql, contract — Grafana asks Loki for `categorize-labels` response encoding; the plan must say it ignores the header and verify two-element `values` still parse

**Problem:** Verified grafana-loki-datasource `pkg/loki/api.go` line 107: every data request carries `X-Loki-Response-Encoding-Flags: categorize-labels`; Loki ≥ 3.0 then returns `values` entries as `[ts, line, {"structuredMetadata":{…},"parsed":{…}}]` plus `data.encodingFlags`. draft-logql.md §L.2.2 fixes `values[i]` to two elements and never mentions the header. Grafana's `frame.go` `parseStats` treats `stats` as optional (line 307 `customMap["stats"]`), and the plugin must still support pre-3.0 Loki without the third element, so two-element values are expected to work — but the plan states no decision and facts-logql does not list it.

**Evidence:** https://raw.githubusercontent.com/grafana/grafana-loki-datasource/main/pkg/loki/api.go (line 107); https://raw.githubusercontent.com/grafana/grafana-loki-datasource/main/pkg/loki/frame.go (lines 302-312)

**Fix:** Add to §L.2.2 rules: "`X-Loki-Response-Encoding-Flags` (Grafana sends `categorize-labels`) is ignored; `values` entries always have two elements and no `encodingFlags` key is emitted — the pre-Loki-3.0 shape every Grafana version accepts." Add to the Phase-1 checklist: run one Explore log query against a real Grafana ≥ 12.3 and confirm lines render (this is the only Loki-shape assumption not provable from source).
