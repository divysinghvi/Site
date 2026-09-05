# Deploy

One static binary (`divy`) serves the API and the embedded SvelteKit site. Two hosting shapes; both use the same two-pass build (API binary → prerender the web app against it → final binary with `internal/web/dist` embedded).

## Vercel (production)

- `vercel.json`: `framework: go`, `buildCommand: bash deploy/vercel-build.sh`, a daily `/api/collect` cron. The Go preset runs `cmd/api/main.go` as one function listening on `$PORT`; everything (HTML, assets, API) goes through it.
- Steps: import the repo (Framework Preset **Go**, Root Directory = repo root) → Storage → **Turso Cloud** integration (injects `TURSO_DATABASE_URL`, `TURSO_AUTH_TOKEN`) → env `SITE_ORIGIN`, `DIVY_COLLECT_TOKEN`, `CRON_SECRET`, `DIVY_GITHUB_TOKEN` → repo secrets `SITE_ORIGIN`, `DIVY_COLLECT_TOKEN` for `.github/workflows/collect.yml` → push.
- Collection: no scheduler in the function. `collect.yml` calls `POST /api/collect` every 5 minutes; the Vercel cron (daily, `CRON_SECRET` as bearer) is the fallback. Rounds are bounded by `DIVY_COLLECT_BUDGET` (8 s) and resumable.
- Storage: without Turso the store is a file under `/tmp` — ephemeral; `/readyz` reports `storage: ephemeral` and the log warns at start.
- Rate limiting and the response cache are per instance. `TRUST_PROXY_HEADERS` defaults to on (Vercel sets `X-Forwarded-For`).
- Deploys happen through Vercel's Git integration; CI never needs a Vercel token.

## Docker / docker compose (local, any VM)

```sh
docker compose up --build            # root compose.yaml includes deploy/docker-compose.yml
```

- `deploy/Dockerfile`: `node:22-bookworm` + the Go 1.24 toolchain for the two passes → `gcr.io/distroless/static-debian12:nonroot`. `ENTRYPOINT ["/divy"]`, default `serve --collect`, `VOLUME /data`, `DIVY_DB_URL=file:/data/divy.db`, `DIVY_CONTENT_DIR=/content`, port 8080. Build args `VERSION`, `COMMIT`, `SITE_ORIGIN` (baked into the prerendered canonical/og tags; default `http://localhost:8080`, override with `DOCKER_SITE_ORIGIN` for compose).
- `deploy/docker-compose.yml`: service `api` on `8080:8080`, named volume `data`, healthcheck `divy ping --url http://127.0.0.1:8080/readyz`, optional root `.env` (`DIVY_GITHUB_TOKEN`, `DIVY_COLLECT_TOKEN`, cadences…); `TURSO_*` passthrough if you want Turso from a container too.
- `make docker` builds the image with the git version tags.

## CI (`.github/workflows/ci.yml`)

Three jobs on push to `main`, pull requests and manual dispatch: **go** (`make lint` = gofmt + vet + golangci-lint v2.5.0, `make test`, `make validate`, `make gen-check`, promtool 3.5.0 from the Prometheus release tarball + `make promtool-check` against a live binary), **web** (`make web-check`, `deploy/vercel-build.sh` exactly as Vercel runs it, then a smoke of the embedded site incl. `X-Divy-Trace-Id`), **docker** (build without push, start the image, `/readyz`).

## Environment

`.env.example` documents every variable with its default. The ones that matter per host:

| Variable | Vercel | Docker |
|---|---|---|
| `PORT` / `DIVY_ADDR` | `PORT` set by the platform | `:8080` |
| `DIVY_DB_URL` / `TURSO_*` | Turso integration | `file:/data/divy.db` |
| `SITE_ORIGIN` | `https://websites-alpha-indol.vercel.app` | `http://localhost:8080` |
| `DIVY_COLLECT_TOKEN`, `CRON_SECRET` | required for `/api/collect` | optional (scheduler runs in-process) |
| `DIVY_GITHUB_TOKEN` | needed for `github_*` series | same |
| `OTEL_*`, `RATE_LIMIT_*`, `RESPONSE_CACHE`, `CORS_ORIGINS`, `TRUSTED_PROXIES`, `TRUST_PROXY_HEADERS` | defaults | defaults |
