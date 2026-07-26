<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- SPDX-FileCopyrightText: 2026 Tommy Bazire -->

# Changelog — EEBUS Bridge add-on

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

_Nothing yet._

## [0.2.0] - 2026-07-26

### Fixed
- **Measurements pushed without a matching description are no longer hidden.**
  This was the root cause of "only a few sensors show up" observed on some
  EEBUS devices (confirmed on a Saunier Duval VR920 heat pump). The vendored
  `eebus-go` `MeasurementCommon.GetDataForFilter` bails with
  `ErrDataNotAvailable` as soon as `MeasurementDescriptionListData` is empty —
  even when raw `MeasurementListData` values are present in the SPINE cache
  (e.g. pushed by the device via a subscription before, or without, their
  descriptions). Added a new `GetRawData()` method that reads
  `MeasurementListData` directly via `featureDataCopyOfType`, bypassing the
  description gate. `renderMeasurementsJSON` / `printMeasurements` now use it,
  so every value present in the cache is published, even without a matching
  description. A measurement `value` of `0` is now correctly serialized
  (previously `omitempty` dropped it and the bridge discarded the line).
- **Per-MeasurementId backfill of values the broad read does not return.**
  Some EEBUS devices reply to a broad `RequestData(nil, nil)` with only a
  subset of the declared measurements — typically the last value that changed
  — even though their `MeasurementDescriptionListData` declares many more.
  Added `backfillMissingMeasurements`: after the broad read, the scanner
  iterates the declared descriptions and, for each `MeasurementId` whose value
  is NOT already cached, issues a targeted `RequestData` with a selector
  pinned to that id. Cheap when the broad read already returned everything
  (no missing ids ⇒ no extra requests); necessary when it did not.
- **SHIP transport logs no longer pollute the NDJSON stream in `-json` mode.**
  Raw SHIP/WebSocket frame dumps emitted by `ship-go` (`websocket.go`,
  `Trace("Send:"/"Recv:", ski, text)`) via the logger wired into
  `service.SetLogging` were going to `os.Stdout` (the default), interleaving
  with the NDJSON data stream and triggering `WARN ndjson: skipping
  unparseable line` in the bridge on every `Send`/`Recv`. The logger is now
  redirected to the same destination as the other loggers (`stderr` in
  `-json` mode). This also closes a minor information-disclosure vector: those
  traces dump the SKI and raw message fragments.

### Added
- **Diagnostic logging for "missing metrics" reports.** Two improvements that
  make it possible to tell, from the add-on log alone, whether a metric the
  device's own app shows is actually exposed over SPINE:
  - `HandleEvent` now logs the entity address and entity type on every SPINE
    event (previously only `entity=<bool>`, which hid WHICH entity a
    `measurementListData` arrived on).
  - `renderMeasurementsJSON` now logs every description the device declares
    (`id=X type=... commodity=... scope=... unit=...`) at debug level, not
    just the count — showing exactly what the device exposes over SPINE, even
    measurements that have no value yet.

## [0.1.3-dev] - 2026-07-20

### Fixed
- Ajout d'une fonction de normalisation des unité de mesure

## [0.1.2-dev] - 2026-07-20

Passage en dev suite à de nombreux bug
### Fixed
modification de la generation des fichier json

## [0.1.2] - 2026-07-20

### Fixed
- correction du fichier de configuration "config.yaml" manque map:

## [0.1.1] - 2026-07-20

### Fixed
- Correxiton de la syntaxe Dockerfile

## [0.1.0] - 2026-07-19

### Added
- **EEBUS Bridge** add-on: pairs with any EEBUS device on the local network
  and exposes its measurements as Home Assistant sensors via MQTT discovery.
- `eebusd` daemon (Go): SHIP/SPINE/mDNS, auto-pairing of discovered devices,
  dynamic entity discovery via SPINE events, NDJSON export on stdout
  (`device`, `manufacturer`, `configuration`, `measurement`, `diagnosis` kinds).
- `eebus-bridge` (Go): consumes the NDJSON stream from `eebusd`, publishes
  Home Assistant MQTT discovery messages and sensor states.
- Multi-arch Docker image (aarch64, amd64, i386), signed with
  Cosign (keyless OIDC).
- Configuration via Home Assistant options (no hardcoded secrets).
- Non-root runtime, host networking (justified by mDNS + inbound SHIP),
  AppArmor left to HA's internal profile.

### Security
- No secrets, certificates or private keys in the image or repository.
- MQTT credentials resolved from the HA Supervisor by default.
- SHIP pairing secret optional, stored as an HA `password` option.
- Persistent state scoped to `/data`.

[Unreleased]: https://github.com/tbazire/homeassistant-addons/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/tbazire/homeassistant-addons/releases/tag/v0.2.0
[0.1.3-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/v0.1.3-dev
[0.1.2-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/v0.1.2-dev
[0.1.2]: https://github.com/tbazire/homeassistant-addons/releases/tag/v0.1.2
[0.1.1]: https://github.com/tbazire/homeassistant-addons/releases/tag/v0.1.1
[0.1.0]: https://github.com/tbazire/homeassistant-addons/releases/tag/v0.1.0
