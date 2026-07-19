<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- SPDX-FileCopyrightText: 2026 Tommy Bazire -->

# Changelog — EEBUS Bridge add-on

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial public release scaffolding (config.yaml, Dockerfile, run.sh, docs).

## [0.1.0] - 2026-07-19

### Added
- **EEBUS Bridge** add-on: pairs with any EEBUS device on the local network
  and exposes its measurements as Home Assistant sensors via MQTT discovery.
- `eebusd` daemon (Go): SHIP/SPINE/mDNS, auto-pairing of discovered devices,
  dynamic entity discovery via SPINE events, NDJSON export on stdout
  (`device`, `manufacturer`, `configuration`, `measurement`, `diagnosis` kinds).
- `eebus-bridge` (Go): consumes the NDJSON stream from `eebusd`, publishes
  Home Assistant MQTT discovery messages and sensor states.
- Multi-arch Docker image (aarch64, amd64, armhf, armv7, i386), signed with
  Cosign (keyless OIDC).
- Configuration via Home Assistant options (no hardcoded secrets).
- Non-root runtime, host networking (justified by mDNS + inbound SHIP),
  AppArmor left to HA's internal profile.

### Security
- No secrets, certificates or private keys in the image or repository.
- MQTT credentials resolved from the HA Supervisor by default.
- SHIP pairing secret optional, stored as an HA `password` option.
- Persistent state scoped to `/data`.

[Unreleased]: https://github.com/tbazire/homeassistant-addons/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/tbazire/homeassistant-addons/releases/tag/v0.1.0
