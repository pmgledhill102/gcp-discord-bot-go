# Container image for the Discord interactions endpoint.
#
# This lives in the repo that owns the source rather than in the repo that
# deploys it. A consumer holding the producer's build instructions has to be
# told when the build changes; a consumer naming a published image tag does not.
#
# Released as ghcr.io/pmgledhill102/gcp-discord-bot-go:<version> by
# .github/workflows/release.yml on a `v*` tag. Build it locally with:
#
#   docker build -t gcp-discord-bot-go .

# Pinned to the build platform, with the target reached through Go's own
# GOOS/GOARCH below rather than through QEMU. CGO is off, so a toolchain running
# natively for the target buys nothing and costs an emulated compile.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder

WORKDIR /app

# Manifests first, so a source-only change reuses the module download layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH

# ./cmd is the main package. The repository root is `package discordbot` -- a
# library whose init() registers the handler with the functions framework -- so
# the entry point is not where a Go build would look by default.
#
# -trimpath drops build-machine paths from the binary; -s -w drops the symbol
# table and DWARF. Together they take about a third off the image, which is a
# third less to pull on the cold start Discord is giving 3 seconds to complete.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /discord-bot ./cmd

# `static` rather than `base`: CGO_ENABLED=0 means the binary needs no libc.
# The image still carries CA certificates, which the Pub/Sub client needs for
# TLS to googleapis.com -- the classic scratch-image failure.
#
# Debian 12 matches the image this service was already being built from. The
# tag is sticky: Dependabot bumps versions inside a tag, and `debian12` is part
# of the repository name, so moving to debian13 is a deliberate edit here.
FROM gcr.io/distroless/static-debian12:nonroot

# Links the GHCR package to this repository. Without it the package is orphaned:
# it does not inherit the repo's access permissions, and the "Source" link on
# the package page goes nowhere.
#
# Permissions, not visibility -- GitHub documents those as separate, and a
# linked package is said to inherit only the former. See the note above the
# push step in .github/workflows/release.yml for what was actually observed.
LABEL org.opencontainers.image.source="https://github.com/pmgledhill102/gcp-discord-bot-go"

COPY --from=builder /discord-bot /discord-bot

# The functions framework listens on $PORT and defaults to 8080. Cloud Run sets
# PORT itself, so this is documentation rather than configuration.
EXPOSE 8080

# Already the default in the :nonroot tag. Stated anyway so that a base image
# change cannot quietly promote the container to root.
USER 65532:65532

ENTRYPOINT ["/discord-bot"]
