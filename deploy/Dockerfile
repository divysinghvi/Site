# syntax=docker/dockerfile:1
# divy.dev — one static binary in a distroless image.
#
# Same two passes as deploy/vercel-build.sh: build the API once, run it as the
# content source while SvelteKit prerenders every page against it, then build
# the final binary with the site embedded (internal/web/dist). Build from the
# repository root:  docker build -f deploy/Dockerfile .   (or `make docker`).

ARG GO_VERSION=1.24
ARG NODE_VERSION=22

# ---- web dependencies (cached separately from the source tree) ----
FROM node:${NODE_VERSION}-bookworm-slim AS web-deps
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

# ---- build: node 22 + the Go 1.24 toolchain, both passes in one stage ----
FROM node:${NODE_VERSION}-bookworm AS build
ARG GO_VERSION
COPY --from=golang:1.24-bookworm /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:/root/go/bin:$PATH \
    CGO_ENABLED=0 \
    GOFLAGS=-trimpath \
    GOTOOLCHAIN=local
ARG VERSION=dev
ARG COMMIT=none
# Origin baked into the prerendered canonical/og tags; override for a public deployment.
ARG SITE_ORIGIN=http://localhost:8080
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-deps /src/web/node_modules ./web/node_modules
RUN set -eu; \
    LDFLAGS="-s -w -X divy.dev/internal/version.Version=${VERSION} -X divy.dev/internal/version.Commit=${COMMIT}"; \
    echo "==> pass 1: API binary (content source for prerendering)"; \
    go build -ldflags "$LDFLAGS" -o /tmp/divy-pass1 ./cmd/api; \
    /tmp/divy-pass1 serve --addr 127.0.0.1:18090 --db file:/tmp/build.db --content ./content --site-origin "${SITE_ORIGIN}" --log-level warn >/tmp/api.log 2>&1 & \
    PID=$!; \
    for i in $(seq 1 30); do /tmp/divy-pass1 ping --url http://127.0.0.1:18090/readyz >/dev/null 2>&1 && break; sleep 1; done; \
    /tmp/divy-pass1 ping --url http://127.0.0.1:18090/readyz >/dev/null || { cat /tmp/api.log; exit 1; }; \
    echo "==> pass 2: prerender the SvelteKit app into internal/web/dist"; \
    (cd web && API_BASE=http://127.0.0.1:18090 PUBLIC_SITE_ORIGIN="${SITE_ORIGIN}" npm run build); \
    kill "$PID" || true; \
    test -f internal/web/dist/index.html; \
    echo "==> pass 3: final binary with the embedded site"; \
    mkdir -p /out /data; \
    go build -ldflags "$LDFLAGS" -o /out/divy ./cmd/api; \
    /out/divy version

# ---- runtime: distroless static, non-root, no shell ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/divy /divy
# content/ is also embedded in the binary; shipping the tree keeps
# DIVY_CONTENT_DIR pointing at real files (no extraction to /tmp at start).
COPY --from=build /src/content /content
# The volume directory must be writable by the nonroot user (uid 65532).
COPY --from=build --chown=nonroot:nonroot /data /data
ENV DIVY_ADDR=:8080 \
    DIVY_DB_URL=file:/data/divy.db \
    DIVY_CONTENT_DIR=/content \
    SITE_ORIGIN=http://localhost:8080
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/divy"]
# `divy serve --collect`: the API plus the in-process collector scheduler.
CMD ["serve", "--collect"]
