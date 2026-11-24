# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e

# The jobtrack image: one static binary (API + embedded web app) on distroless.
#
#   docker build --build-arg VERSION=$(git rev-parse --short HEAD) -t jobtrack .
#
# The build stages run on the builder's own platform ($BUILDPLATFORM) and Go
# cross-compiles to the target, so CI's x86_64 runners make the Pi's arm64 image natively
# (no slow QEMU emulation). The web app is plain files, the same on every platform.
#
# Base images are pinned by digest, so a rebuild uses exactly the same bases; Dependabot
# proposes updates once they've been out for two weeks.

# --- web app: Vite + React, built to static files ---
FROM --platform=$BUILDPLATFORM node:26.13.1-trixie-slim@sha256:e6bedef217677b276c591795e169840edd9096dbf13f29df8ef8d1856b8820bc AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json web/.npmrc ./
# Exactly the locked versions; install scripts are off (web/.npmrc).
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
# vite builds into ../internal/webui/dist, where the Go build embeds it from.
RUN npm run build

# --- Go binary ---
FROM --platform=$BUILDPLATFORM golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d AS build
WORKDIR /src
# Modules first: this layer is reused until go.mod or go.sum change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
ARG TARGETOS TARGETARCH VERSION=dev
# CGO off: a static binary that needs no C library, so it runs on distroless/static.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -tags webui -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/jobtrack ./cmd/api

# --- runtime: no shell, no package manager, just CA certificates and the binary ---
FROM gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5
COPY --from=build /out/jobtrack /jobtrack
# nonroot is uid 65532: the service never runs as root.
USER nonroot:nonroot
# Inside the container it listens on every interface; the host publishes the port on
# 127.0.0.1 only (deploy/compose.yml), behind tailscale serve. JOBTRACK_AUTH=off is
# refused on this address, so the image always checks Tailscale logins.
ENV JOBTRACK_ADDR=0.0.0.0:8090
EXPOSE 8090
# The binary checks itself: there's no curl in the image.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/jobtrack", "healthcheck"]
# `docker run IMAGE` serves; `docker run IMAGE migrate up` migrates.
ENTRYPOINT ["/jobtrack"]
