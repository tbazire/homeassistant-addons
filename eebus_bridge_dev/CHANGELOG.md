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

## [0.3.0-dev] - 2026-07-25

### Fixed
- **Measurements without a matching description are no longer hidden.** This was
  the root cause of the "only 6 sensors show up" issue observed on the Saunier
  Duval VR920 heat pump. `MeasurementCommon.GetDataForFilter` (in the vendored
  `eebus-go` lib) bails with `ErrDataNotAvailable` as soon as
  `MeasurementDescriptionListData` is empty — even when raw
  `MeasurementListData` values are present in the SPINE cache (e.g. pushed by
  the device via a subscription before, or without, their descriptions). The
  scanner's `renderMeasurementsJSON` therefore only ever emitted the subset of
  values whose description had already arrived, dropping the rest.
  The fix adds a new `MeasurementCommon.GetRawData()` method that reads
  `MeasurementListData` directly from the SPINE feature cache, bypassing the
  description gate. `renderMeasurementsJSON` (and the text-mode
  `printMeasurements`) now iterate over `GetRawData()`, so every value the
  device exposes is emitted — enriched with `type` / `commodity` / `scope` /
  `unit` from the description when present, emitted without those fields when
  the description is absent (the bridge still creates a sensor, named after the
  measurement id).

### Added
- `MeasurementCommon.GetRawData()` and the corresponding
  `MeasurementCommonInterface` entry (in the locally-replaced `eebus-go`).
- Regression tests `Test_GetRawData_ReturnsEmptyWhenNoData` and
  `Test_GetRawData_ReturnsValuesWithoutDescriptions` in
  `eebus-go/features/internal/measurement_test.go`, covering the exact
  scenario (values present, descriptions absent) that was failing on the VR920.

### Notes
- Audit finding (documented for future work): the `ElectricalConnection`
  feature carries only wiring metadata, permitted value ranges and nameplate
  characteristics — NOT live voltage/current/frequency values. Those live
  exclusively in the `Measurement` feature, so no `ElectricalConnection`
  rendering is needed to recover the missing telemetry. Exposing EC limits /
  characteristics as HA sensors is left for a later, separate change.

## [0.2.0-dev] - 2026-07-25

### Fixed
- **Zero-value measurements are now published** (was the most impactful bug).
  The `Value` field carried `omitempty` on both sides of the NDJSON wire
  (`eebusd` emitter and `eebus-bridge` parser). As a result any measurement
  equal to exactly `0` (idle power on a heat pump, empty energy counter,
  0 °C, 0 A current, …) was silently dropped — `eebusd` omitted the `value`
  key from the JSON line, and the bridge then treated the line as "missing
  value" and discarded it. On devices like the Saunier Duval VR920 that
  report many channels at 0 when idle, this hid a large share of the
  available metrics. The fix:
  - `eebusd/internal/scanner/export.go`: `measurementLine.Value` no longer
    uses `omitempty` — every emitted line carries a concrete number.
  - `bridge/internal/ndjson.go`: `Measurement.Value` is now `*float64` so
    the parser can distinguish "field absent / JSON null" (no measurement
    yet → drop) from "value is 0" (a real measurement → publish).
  - `bridge/internal/discovery.go`: dereferences `*me.Value` accordingly.

- **Measurements without a description are no longer hidden.** Previously,
  if `MeasurementDescriptionListData` had not arrived yet (early pull) or
  the device did not expose one, `renderMeasurementsJSON` returned early
  and emitted nothing — even when raw `MeasurementListData` was available
  (e.g. pushed via a subscription). The function now renders any data that
  carries a real value, using the description only to enrich `type`,
  `commodity`, `scope` and `unit` when present.

- **Data without a real value is now skipped explicitly.** A `MeasurementData`
  entry whose `Value` is nil is no longer rendered as `0` (which would be a
  fake zero, indistinguishable from a real one); it is dropped with a debug
  log. Only entries that actually carry a `ScaledNumber` value are emitted.

### Added
- Debug log in `renderMeasurementsJSON`: number of descriptions and number
  of values seen per render pass — makes it much easier to diagnose "why is
  metric X missing" directly from the add-on log.
- Regression tests:
  - `TestParserKeepsZeroValue`, `TestParserKeepsNegativeValue`,
    `TestParserKeepsZeroAfterMalformed` (bridge NDJSON parser).
  - `TestOnMeasurementZeroValuePublishesState` (bridge discovery mapper).
  - `TestMeasurementLineAlwaysCarriesValue` (eebusd emitter).

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

[Unreleased]: https://github.com/tbazire/homeassistant-addons/compare/dev-v0.3.0...HEAD
[0.3.0-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.3.0
[0.2.0-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.2.0
[0.1.0-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.1.0
