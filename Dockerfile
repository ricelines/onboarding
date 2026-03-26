# syntax=docker/dockerfile:1.10

ARG GO_VERSION=1.26.1

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-trixie AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

ENV CGO_ENABLED=0 \
    GOMODCACHE=/go/pkg/mod \
    GOCACHE=/root/.cache/go-build

COPY --link go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    go mod download

COPY --link cmd ./cmd
COPY --link internal ./internal

RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build \
        -buildvcs=false \
        -trimpath \
        -ldflags='-s -w' \
        -o /out/onboarding-provisioner \
        ./cmd/onboarding-provisioner

RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build \
        -buildvcs=false \
        -trimpath \
        -ldflags='-s -w' \
        -o /out/onboarding-test-tcp-forwarder \
        ./cmd/onboarding-test-tcp-forwarder

RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build \
        -buildvcs=false \
        -trimpath \
        -ldflags='-s -w' \
        -o /out/onboarding-test-a2a-get-task \
        ./cmd/onboarding-test-a2a-get-task

RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian13:nonroot AS runtime

WORKDIR /app

COPY --from=build --chown=65532:65532 /out/onboarding-provisioner /app/onboarding-provisioner
COPY --from=build --chown=65532:65532 /out/onboarding-test-tcp-forwarder /app/onboarding-test-tcp-forwarder
COPY --from=build --chown=65532:65532 /out/onboarding-test-a2a-get-task /app/onboarding-test-a2a-get-task
COPY --from=build --chown=65532:65532 /out/data /app/data

EXPOSE 8080

ENTRYPOINT ["/app/onboarding-provisioner"]
