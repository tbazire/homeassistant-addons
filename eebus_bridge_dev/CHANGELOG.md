<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- SPDX-FileCopyrightText: 2026 Tommy Bazire -->

# Changelog — EEBUS Bridge (Dev) add-on

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This is the **development channel** of EEBUS Bridge. It ships the same Go code
as the production add-on (`eebus_bridge/`) and is used to preview upcoming
changes before they land in a production release.

> ⚠️ The dev and prod add-ons are **mutually exclusive**: they share the same
> SHIP port (4711), the same MQTT ClientID and the same HA discovery topic
> namespace. Only one of them can run at a time on a given Home Assistant
> instance.

Release tags for this channel use the `dev-v*` prefix (e.g. `dev-v0.1.0`),
distinct from the production `v*` tags, so each channel triggers only its own
CI workflow.

## [Unreleased]

_Nothing yet._

## [0.1.0-dev] - 2026-07-25

### Added
- **EEBUS Bridge (Dev)** add-on: development channel of EEBUS Bridge.
- Initial fork from the production add-on at version `0.1.3-dev` of
  `eebus_bridge/`. Same Go code (`eebusd` daemon + `eebus-bridge` MQTT/HA
  bridge), same SHIP/SPINE/mDNS behavior.
- Separate slug (`eebus_bridge_dev`), image (`{arch}-eebus-bridge-dev`) and
  GitHub Release tag prefix (`dev-v*`) so both channels can coexist in the
  HA add-on store without interfering with each other's CI.

### Notes
- This channel exists so users can test upcoming features and report bugs
  before they reach the production add-on.
- The code source is intentionally kept identical to the production add-on
  at fork time. Future dev-only changes will be listed here.

[Unreleased]: https://github.com/tbazire/homeassistant-addons/compare/dev-v0.1.0...HEAD
[0.1.0-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.1.0
