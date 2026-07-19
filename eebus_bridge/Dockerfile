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
# Both Go binaries are built with CGO_ENABLED=0 so they are fully static and
# work on musl-based Alpine images. The runtime stage only needs to copy them
# in plus the s6 service script.

ARG BUILD_FROM
# hadolint ignore=DL3006
FROM ${BUILD_FROM} AS eebusd-builder

# Install Go in the builder stage. The HA base images are Alpine; use apk.
# We pin Go 1.24 to match go.mod.
RUN apk add --no-cache go=1.24.*

WORKDIR /src/eebusd
# Copy the vendored module first so dependency changes are a separate layer.
COPY eebusd/go.mod eebusd/go.sum ./
COPY eebusd/vendor/ ./vendor/
COPY eebusd/cmd/ ./cmd/
COPY eebusd/internal/ ./internal/

RUN CGO_ENABLED=0 GOOS=linux GOFLAGS=-mod=vendor \
    go build -trimpath -ldflags="-s -w" \
    -o /out/eebusd ./cmd/scanner


# hadolint ignore=DL3006
FROM ${BUILD_FROM} AS bridge-builder

RUN apk add --no-cache go=1.24.*

WORKDIR /src/bridge
COPY bridge/go.mod bridge/go.sum ./
# bridge/ has no vendor/ — deps are fetched from the module proxy at build.
# If the build environment is offline, vendor the bridge too.
COPY bridge/cmd/ ./cmd/
COPY bridge/internal/ ./internal/

# Download deps explicitly so the layer is cached across code changes.
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" \
    -o /out/eebus-bridge ./cmd/bridge


# ---- Runtime stage ----------------------------------------------------------
# hadolint ignore=DL3006
FROM ${BUILD_FROM}

# Drop to a non-root user. HA add-ons normally run as root, but the add-on
# needs no privileged capability (host_network is enough for EEBUS), so we
# create a dedicated unprivileged account.
# The /data mount is provided by HA and is chowned to root by the Supervisor;
# we keep the runtime user non-root and grant it write access via the group.
RUN addgroup -S eebus && adduser -S -G eebus -h /app eebus \
    && mkdir -p /app /data/eebus \
    && chown -R root:eebus /data/eebus \
    && chmod 0770 /data/eebus

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

# Run as the non-root user. The base image's ENTRYPOINT [/init] (s6-overlay)
# remains the PID 1 and supervises our service.
USER eebus

# No CMD/ENTRYPOINT: provided by the HA base image (s6-overlay v3 [/init]).
