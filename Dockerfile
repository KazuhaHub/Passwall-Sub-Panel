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
# internal/seed/) and released into /app/config on first start, so no
# `/app/defaults/` baking or entrypoint shim is needed.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
# Pin the panel process to UTC so Go's time.Local matches the
# DB DSN's loc=UTC. The configurable "panel timezone" (Asia/Shanghai
# etc.) is applied per-call via paneltz.Now for business calendar
# math; the underlying clock and stored DATETIMEs stay UTC.
ENV TZ=UTC
WORKDIR /app
COPY --from=go-builder /out/psp /app/psp
RUN chmod +x /app/psp && mkdir -p /app/config /app/data
EXPOSE 8788
# Runs as root (no USER directive) so a bind-mounted /app/config — which Docker
# auto-creates root-owned, and which the host owns — is always writable for
# config.yaml generation, seed release, and the geoip dir. Same approach as the
# 3X-UI / Cloudreve images. Keep this runtime stage in sync with Dockerfile.release.
ENTRYPOINT ["/app/psp"]
