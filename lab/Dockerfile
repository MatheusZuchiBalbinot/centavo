# syntax=docker/dockerfile:1

# The panel is static files, built once on the build platform.
FROM --platform=$BUILDPLATFORM node:22.23.3-trixie-slim@sha256:154ba2f4d6fec323d28e4f4bb86bba4677f1223391a1979cf521304e03a98dfa AS web

WORKDIR /src/web

COPY web/package.json web/package-lock.json web/.npmrc ./
RUN npm ci --no-audit --no-fund

COPY web/ ./
RUN npm run build

# Cross-compiles on the build platform (no emulation) for every target platform.
FROM --platform=$BUILDPLATFORM golang:1.26.10-trixie@sha256:909771fef53e3dcb0950255f0c9a8b4728c3b841e61509c7b29b098de7ee02d4 AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src

COPY go.* ./
RUN go mod download

COPY . .
COPY --from=web /src/internal/dashboard/web/app ./internal/dashboard/web/app
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOWORK=off \
    go build -trimpath \
    -ldflags "-s -w -X centavo/lab/internal/platform/buildinfo.version=$VERSION" \
    -o /out/lab ./cmd/lab
# The rounds volume takes its owner from this directory on first use.
RUN mkdir -p /out/data/rounds /out/events

FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3

COPY --from=build /out/lab /app/lab
COPY --from=build --chown=nonroot:nonroot /out/data /data
COPY --from=build --chown=nonroot:nonroot /out/events /lab/events

USER nonroot:nonroot

ENTRYPOINT ["/app/lab"]
