# syntax=docker/dockerfile:1

# Cross-compiles on the build platform (no emulation) for every target platform.
FROM --platform=$BUILDPLATFORM golang:1.26.9-trixie AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src

COPY go.* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOWORK=off \
    go build -trimpath \
    -ldflags "-s -w -X centavo/lab/internal/platform/buildinfo.version=$VERSION" \
    -o /out/lab ./cmd/lab

FROM gcr.io/distroless/static-debian13:nonroot

COPY --from=build /out/lab /app/lab

USER nonroot:nonroot

ENTRYPOINT ["/app/lab"]
