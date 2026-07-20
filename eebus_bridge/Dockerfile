# syntax=docker/dockerfile:1
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: 2026 Tommy Bazire
#
# Multi-stage Dockerfile for the eebus_bridge add-on.
#
# Stages:
#   1. eebusd-builder   : builds the static eebusd binary from vendored deps.
#   2. bridge-builder   : builds the static eebus-bridge binary.
#   3. runtime          : HA base image (s6-overlay v3), non-root, minimal.
#
# Builders use the official golang image (pinned to 1.24, alpine-based) instead
# of `apk add go` on the HA base. Reason: the Go version shipped by Alpine 3.21
# community (1.23.5) is older than the `go 1.24.1` directive in go.mod, which
# would fail the build. The golang image is kept current upstream and tracks
# Go patch releases. The produced binaries are fully static (CGO_ENABLED=0)
# and run on any Linux — including the musl-based HA runtime base.
#
# The HA base image (BUILD_FROM, per-arch) is used ONLY for the runtime stage,
# where s6-overlay + bashio are required.

# ---- Stage 1: eebusd builder ------------------------------------------------
# We use golang:1.24-alpine (official, multi-arch via QEMU on the CI runner).
# hadolint ignore=DL3007
ARG BUILD_FROM
ARG BUILD_ARCH
ARG BUILD_VERSION
FROM golang:1.24-alpine AS eebusd-builder

WORKDIR /src/eebusd
# Copy the vendored module first so dependency changes are a separate layer.
COPY eebusd/go.mod eebusd/go.sum ./
COPY eebusd/vendor/ ./vendor/
COPY eebusd/cmd/ ./cmd/
COPY eebusd/internal/ ./internal/

# CGO disabled => fully static binary, musl-compatible, runs on HA alpine base.
# GOFLAGS=-mod=vendor forces offline build (no module proxy call).
RUN CGO_ENABLED=0 GOOS=linux GOFLAGS=-mod=vendor \
    go build -trimpath -ldflags="-s -w" \
    -o /out/eebusd ./cmd/scanner


# ---- Stage 2: eebus-bridge builder ------------------------------------------
# hadolint ignore=DL3007
FROM golang:1.24-alpine AS bridge-builder

WORKDIR /src/bridge
COPY bridge/go.mod bridge/go.sum ./
# bridge/ has no vendor/ — deps are fetched from the Go module proxy at build.
COPY bridge/cmd/ ./cmd/
COPY bridge/internal/ ./internal/

# Download deps explicitly so the layer is cached across code changes.
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" \
    -o /out/eebus-bridge ./cmd/bridge


# ---- Stage 3: runtime (HA add-on base) --------------------------------------
# BUILD_FROM is injected by the CI workflow / HA builder, per architecture.
# It resolves to ghcr.io/home-assistant/<arch>-base:3.21 (Alpine 3.21 + s6 v3).
# hadolint ignore=DL3006
FROM ${BUILD_FROM}
ARG BUILD_FROM
ARG BUILD_VERSION

# Drop to a non-root user. HA add-ons normally run as root, but the add-on
# needs no privileged capability (host_network is enough for EEBUS), so we
# create a dedicated unprivileged account.
# The /data mount is provided by HA and is chowned to root by the Supervisor;
# we keep the runtime user non-root and grant it write access via the group.
RUN addgroup -S eebus && adduser -S -G eebus -h /app eebus \
    && mkdir -p /app

# Copy the static binaries in.
COPY --from=eebusd-builder /out/eebusd /usr/local/bin/eebusd
COPY --from=bridge-builder /out/eebus-bridge /usr/local/bin/eebus-bridge

# s6-overlay service script: run.sh is what HA launches.
COPY rootfs/ /
RUN chmod +x /etc/services.d/eebus_bridge/run \
    && chmod +x /usr/local/bin/eebusd /usr/local/bin/eebus-bridge

# OCI + HA labels.
LABEL io.hass.name="EEBUS Bridge" \
      io.hass.description="Generic EEBUS bridge for Home Assistant" \
      io.hass.type="addon" \
      org.opencontainers.image.title="EEBUS Bridge" \
      org.opencontainers.image.description="Generic EEBUS bridge for Home Assistant" \
      org.opencontainers.image.source="https://github.com/tbazire/homeassistant-addons" \
      org.opencontainers.image.licenses="Apache-2.0"

# No CMD/ENTRYPOINT: provided by the HA base image (s6-overlay v3 [/init]).
