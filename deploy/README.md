# deploy/

- `Dockerfile` — multi-stage (node 22 + Go 1.24 → distroless static, non-root): the same two passes as `vercel-build.sh`, then `ENTRYPOINT ["/divy"]`, `CMD ["serve","--collect"]`, `VOLUME /data`. Build from the repository root: `docker build -f deploy/Dockerfile .` or `make docker`.
- `docker-compose.yml` — the `api` service on :8080 with the `data` volume and a `/readyz` healthcheck; the root `compose.yaml` includes it, so `docker compose up` works from the repository root.
- `vercel-build.sh` — the Vercel build command (`vercel.json`): API binary → prerender the SvelteKit app against it → final binary at `$VERCEL_OUTPUT_FILE`.

Hosting is Vercel (no custom domain; `SITE_ORIGIN` is the origin) with Turso for the time series; the Docker files are the brief's `docker compose up` for local runs and any VM. Steps and environment: [../docs/deploy.md](../docs/deploy.md).
