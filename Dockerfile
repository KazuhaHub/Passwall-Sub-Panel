# Stage 1 — build the React SPA bundle.
FROM node:20-alpine AS web-builder
WORKDIR /web
COPY web-react/package.json web-react/package-lock.json* ./
RUN if [ -f package-lock.json ]; then npm ci; else npm install; fi
COPY web-react/ ./
# Vite writes into ../internal/web/dist by config, but inside this stage we
# only have /web. Redirect outDir to the local dist/ here, then copy across
# stages.
RUN npx vite build --outDir /web/dist --emptyOutDir

# Stage 2 — build the Go binary with the SPA assets embedded.
FROM golang:1.26-alpine AS go-builder
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Drop the SPA bundle where //go:embed expects it.
RUN rm -rf internal/web/dist && mkdir -p internal/web/dist
COPY --from=web-builder /web/dist/ ./internal/web/dist/
# Build identity — passed in by CI (or `docker build --build-arg`). Defaults
# keep manual builds working but leave the binary visibly "dev" so a stray
# build is easy to spot. `internal/version.Version` is the lookup the
# /api/version handler and the SPA About badge read.
ARG VERSION=dev
ARG COMMIT=""
ARG BUILD_DATE=""
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w \
      -X github.com/KazuhaHub/passwall-sub-panel/internal/version.Version=${VERSION} \
      -X github.com/KazuhaHub/passwall-sub-panel/internal/version.Commit=${COMMIT} \
      -X github.com/KazuhaHub/passwall-sub-panel/internal/version.BuildDate=${BUILD_DATE}" \
    -o /out/psp ./cmd/panel

# Stage 3 — minimal runtime.
# Default rulesets and templates are embedded into the binary (see
# internal/seed/) and released into /app/config on first start.
#
# The image STARTS as root and docker-entrypoint.sh self-heals ownership of the
# bind-mounted /app/config (Docker never chowns a bind mount, so a fresh or
# upgrade-leftover root-owned ./config would otherwise make the non-root UID
# crash-loop), then drops to non-root via su-exec before exec'ing the panel.
# su-exec is Alpine's ~10KB gosu equivalent; PUID/PGID (default 10001) align the
# runtime UID with the host owner so ./config stays host-editable.
# NOTE: keep this runtime stage in sync with Dockerfile.release (drift guard).
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata su-exec \
 && adduser -D -H -u 10001 psp
# Pin the panel process to UTC so Go's time.Local matches the
# DB DSN's loc=UTC. The configurable "panel timezone" (Asia/Shanghai
# etc.) is applied per-call via paneltz.Now for business calendar
# math; the underlying clock and stored DATETIMEs stay UTC.
ENV TZ=UTC
WORKDIR /app
COPY --from=go-builder /out/psp /app/psp
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /app/psp /usr/local/bin/docker-entrypoint.sh \
 && mkdir -p /app/config /app/data && chown -R psp:psp /app
EXPOSE 8788
# The long-lived process runs non-root (UID 10001 by default; override via
# PUID/PGID). The entrypoint starts as root only to chown the mounts, then
# su-exec drops privileges. Set PUID/PGID to your host `id -u`/`id -g` for
# sudo-free host edits of ./config.
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
