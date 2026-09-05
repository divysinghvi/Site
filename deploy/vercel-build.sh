#!/usr/bin/env bash
# Vercel build (Go framework preset, standalone server mode).
# Two passes: build the API once, run it as the content source while SvelteKit
# prerenders every page against it, then build the final binary with the site
# embedded (internal/web/dist) into $VERCEL_OUTPUT_FILE. Same steps as
# `make vercel-build`, without depending on make in the build image.
set -euo pipefail
cd "$(dirname "$0")/.."

: "${VERCEL_OUTPUT_FILE:=bin/divy}"
ADDR="127.0.0.1:18090"
ORIGIN="${SITE_ORIGIN:-}"
if [ -z "$ORIGIN" ] && [ -n "${VERCEL_PROJECT_PRODUCTION_URL:-}" ]; then ORIGIN="https://${VERCEL_PROJECT_PRODUCTION_URL}"; fi
if [ -z "$ORIGIN" ] && [ -n "${VERCEL_URL:-}" ]; then ORIGIN="https://${VERCEL_URL}"; fi
: "${ORIGIN:=http://localhost:8080}"
VERSION="${VERCEL_GIT_COMMIT_REF:-dev}"
COMMIT="${VERCEL_GIT_COMMIT_SHA:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
LDFLAGS="-s -w -X divy.dev/internal/version.Version=${VERSION} -X divy.dev/internal/version.Commit=${COMMIT}"

echo "==> go $(go version | awk '{print $3}') / node $(node --version); site origin ${ORIGIN}"
echo "==> pass 1: API binary (content source for prerendering)"
go build -ldflags "$LDFLAGS" -o /tmp/divy-pass1 ./cmd/api

TMP="$(mktemp -d)"
/tmp/divy-pass1 serve --addr "$ADDR" --db "file:${TMP}/build.db" --content ./content --site-origin "$ORIGIN" >"${TMP}/api.log" 2>&1 &
PID=$!
trap 'kill "$PID" 2>/dev/null || true' EXIT
for i in $(seq 1 30); do
  if /tmp/divy-pass1 ping --url "http://${ADDR}/readyz" >/dev/null 2>&1; then break; fi
  sleep 1
done
/tmp/divy-pass1 ping --url "http://${ADDR}/readyz" >/dev/null || { cat "${TMP}/api.log"; exit 1; }

echo "==> pass 2: prerender the SvelteKit app into internal/web/dist"
( cd web && npm ci --no-audit --no-fund && API_BASE="http://${ADDR}" PUBLIC_SITE_ORIGIN="$ORIGIN" npm run build )
kill "$PID" 2>/dev/null || true
trap - EXIT
test -f internal/web/dist/index.html

echo "==> pass 3: final binary with the embedded site -> ${VERCEL_OUTPUT_FILE}"
mkdir -p "$(dirname "$VERCEL_OUTPUT_FILE")"
go build -ldflags "$LDFLAGS" -o "$VERCEL_OUTPUT_FILE" ./cmd/api
ls -la "$VERCEL_OUTPUT_FILE"
