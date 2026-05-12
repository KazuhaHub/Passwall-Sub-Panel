# Stage 1 — build the Vue SPA bundle.
FROM node:20-alpine AS web-builder
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN if [ -f package-lock.json ]; then npm ci; else npm install; fi
COPY web/ ./
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
# Build both binaries.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/panel ./cmd/panel
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/psp-cli ./cmd/cli

# Stage 3 — minimal runtime.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S psp && adduser -S -G psp psp
WORKDIR /app
COPY --from=go-builder /out/panel /app/panel
COPY --from=go-builder /out/psp-cli /app/psp-cli
USER psp
EXPOSE 8787
ENTRYPOINT ["/app/panel"]
