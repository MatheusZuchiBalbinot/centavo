# syntax=docker/dockerfile:1

# Cross-compiles on the build platform (no emulation) for every target platform.
FROM --platform=$BUILDPLATFORM golang:1.26.9-trixie@sha256:f06b7122f888680048afb247c4e5f49c0069eb6c4c1ccae0f3f4834527a8d22c AS build

ARG TARGETOS
ARG TARGETARCH
# "chaoslab" compiles in the chaos code; empty builds the production binary.
ARG BUILD_TAGS=""
ARG VERSION=dev

WORKDIR /src

COPY go.* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOWORK=off \
    go build -trimpath -tags "$BUILD_TAGS" \
    -ldflags "-s -w -X centavo/bank/internal/platform/buildinfo.version=$VERSION" \
    -o /out/bank ./cmd/bank
# The shared events volume takes its owner from this directory on first use.
RUN mkdir -p /out/events

FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3

COPY --from=build /out/bank /app/bank
COPY --from=build --chown=nonroot:nonroot /out/events /lab/events

USER nonroot:nonroot

ENTRYPOINT ["/app/bank"]
