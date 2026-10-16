# syntax=docker/dockerfile:1

# Cross-compiles on the build platform (no emulation) for every target platform.
FROM --platform=$BUILDPLATFORM golang:1.26.9-trixie AS build

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
    -ldflags "-s -w -X centavo/partner/internal/platform/buildinfo.version=$VERSION" \
    -o /out/partner ./cmd/partner
# The shared events volume takes its owner from this directory on first use.
RUN mkdir -p /out/events /out/data

FROM gcr.io/distroless/static-debian13:nonroot

COPY --from=build /out/partner /app/partner
COPY --from=build --chown=nonroot:nonroot /out/events /lab/events
COPY --from=build --chown=nonroot:nonroot /out/data /data

USER nonroot:nonroot

ENTRYPOINT ["/app/partner"]
