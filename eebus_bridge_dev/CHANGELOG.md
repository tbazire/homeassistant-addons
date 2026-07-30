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

## [0.5.1-dev] - 2026-07-30

Small follow-up to 0.5.0-dev, addressing an OHPCF audit: the climate entity now
only exposes controls the device actually supports, instead of a hardcoded list.

### Fixed

- **OHPCF actions are now per-entity, not hardcoded.** Previously the `pause` /
  `resume` presets and the `abort` mode were always offered, even when the
  device advertised `isPausable=false` or `isStoppable=false` (OHPCF-011/6 and
  OHPCF-011/5). Selecting an unsupported control would always be rejected by
  the device. The `WriteUseCase` interface's `AvailableActions()` is now
  `AvailableActionsForEntity(entity)`: OHPCF derives `pause`/`resume` from
  `ConsumptionIsPausable` and `abort` from `ConsumptionIsStoppable`, keeping
  `schedule` always available. When the capability data is not yet known
  (device still announcing), the full list is returned as a safe fallback.
- **LPC** returns its static `[set, clear]` list (no per-entity capability
  flags in the LPC use case).

### Notes

- This does not change the wire contract (the `controllable` line still carries
  the filtered `actions` array); the bridge was already generic over it.
- An OHPCF audit (cross-checked against the Saunier Duval/Bosch SDBG heat-pump
  manual `eebusManual.pdf` §1.3.1–1.3.4 and the eebus-go OHPCF surface)
  confirmed the four control verbs are fully covered; the per-entity filtering
  was the one concrete gap. The eight OHPCF read signals (requested/max power,
  start time, pausable/stoppable, min run/pause durations) are still pending a
  future read-side exposure lot.

## [0.5.0-dev] - 2026-07-30

This release adds the **LPC** (Limitation of Power Consumption) write use case
on top of the generic write pipeline introduced in 0.4.0-dev, and extends the
contract so any future numeric use case (LPP, OPEV current limit, …) carries
its own unit of measurement. No behaviour change for read-only deployments or
for OHPCF users.

### Added

- **LPC use case (power consumption limit).** When a paired device exposes the
  `LoadControl` feature — heat pumps, wallboxes, inverters, batteries,
  sub-meters, any controllable system — a `number` entity appears in Home
  Assistant representing the active power limit in watts (W). Setting a value
  caps the device's consumption; clearing it removes the cap. A value of `0`
  or a non-positive input is interpreted as "clear" (SPINE LoadControl limits
  are absolute magnitudes). The use case is generic: it targets whatever
  entity advertises `LoadControl`, regardless of brand.
- **Per-use-case unit of measurement.** The `WriteUseCase` interface gained a
  `HAUnit()` method, and the `controllable` NDJSON line now carries an optional
  `unit` field. The unit (W for LPC, A for a future current-limit use case, °C
  for a setpoint, …) flows from the use case module → eebusd → bridge → HA
  discovery, so the slider is labelled correctly for any device family.
  Climate/switch/select use cases return an empty unit.
- **Bridge: `number` discovery + command routing.** `OnControllable` builds a
  `HANumber` entity for `component: "number"`, subscribes to its `value/cmd`
  topic, and `decodeHACommand` routes `/value/cmd` payloads to `<uc>.set` (with
  the parsed float) or `<uc>.clear` (empty payload). Non-numeric payloads are
  rejected so no garbage value reaches eebusd.

### Architecture

- The LPC module (`eebusd/internal/writes/lpc/`) wraps eebus-go's
  `usecases/eg/lpc` (Energy Guard actor = our CEM role) and reuses the exact
  registry/dispatch/result-callback pattern from OHPCF — confirming the
  modular design's promise: adding a use case is one self-contained package
  plus one blank import in `writes/bind.go`, with zero change to the dispatcher
  or the bridge's component-agnostic discovery.

### Notes

- LPC applies to a broad device range (any controllable load, not just heat
  pumps). The VR920 is one example; wallboxes, inverters and batteries expose
  the same `LoadControl` feature and will light up the same `number` entity.
- Time-bounded limits (a cap that auto-expires after N minutes) are
  intentionally out of scope here; they would need a duration field on the wire
  contract and can be added later without disturbing the persistent-limit path.
- LPP (production limit), OPEV/OSCEV (EV charging) remain planned — the
  registry is ready for them.

## [0.4.0-dev] - 2026-07-26

This release opens the **write/control channel**: the add-on is no longer
read-only. When `write.enable` is set, the bridge can drive the device from
Home Assistant (schedule / pause / resume / abort a heat-pump compressor via
OHPCF), with an architecture designed to grow without touching the bridge.

### Added

- **Generic write pipeline (backchannel stdin NDJSON).** The bridge now opens
  `eebusd`'s stdin and writes `command` lines back to it, symmetric to the
  existing read-side stream. Two new inbound kinds were added to the NDJSON
  contract: `controllable` (a device announced support for a write use case)
  and `command_result` (outcome of a dispatched command). The read-side kinds
  are unchanged; the parser ignores unknown kinds, so older eebusd binaries
  remain compatible.
- **Modular write use-case registry (`eebusd/internal/writes/`).** Each write
  use case lives in its own sub-package and self-registers at init time via a
  common `WriteUseCase` interface. Adding LPC / LPP / OPEV / OSCEV later is a
  new sub-package + one blank import line in `writes/bind.go` — zero change
  to the dispatcher or the bridge. This keeps the add-on generic: it targets
  any EEBUS device that advertises the use case, not a specific brand.
- **OHPCF use case (heat-pump compressor flexibility).** When a paired device
  exposes the `SmartEnergyManagementPs` feature (e.g. a Saunier Duval/Vaillant
  VR920), a `climate` entity appears in Home Assistant. Modes map to
  `Abort` (off) / `Schedule` (auto); presets map to `Pause` / `Resume`. The
  entity's `action` reflects the real compressor state. If the device rejects
  a command, the action stays unchanged and the bridge log explains why.
- **Configuration: `write.enable` / `write.use_cases` / `write.device_profile`.**
  `write.enable` is `false` by default — the add-on stays strictly read-only
  out of the box. `use_cases: auto` activates every use case the device
  announces; an explicit list restricts it. `device_profile` filters discovery
  by device family (`heatpump` / `evse` / `inverter` / `battery` / `generic`).
- **Documentation: new "Write commands" section in `DOCS.md`** describing the
  pipeline, configuration knobs, and the supported/planned use cases. The
  README also gained a "Controlling devices" section.

### Fixed

- **MQTT subscriptions are now re-applied on reconnect.** The paho client does
  not remember subscriptions across reconnects when auto-reconnect is enabled;
  without this fix, a command topic we subscribed to before a network blip
  would silently stop firing. The bridge now re-subscribes every active topic
  in its `OnConnect` handler.

### Security

- `write.enable = false` by default: no command surface is exposed unless the
  operator explicitly opts in (principle of least permission).
- The dispatcher validates `IsCompatible(entity)` before every write — a write
  can never reach an entity that did not advertise the use case.
- Subscriptions target only the command topics the bridge itself announced
  (no wildcards), so external injection is not possible.
- `command_result.error` messages carry generic SPINE reasons only; SKIs are
  masked to their tail in error lines.
- No new Docker permission, no new dependency, no hardcoded secret.

### Notes

- OHPCF is the only write use case shipped in this release because it is the
  only one clearly announced by the VR920 (its `smartEnergyManagementPs` +
  `powerSequence*` data were confirmed by the reference script). LPC, LPP,
  OPEV and OSCEV will follow as self-contained modules reusing the same
  pipeline; they are most relevant for wallboxes, inverters and batteries.
- Setpoint HVAC (writing a target temperature in °C) is **not** covered here:
  the locally-replaced `eebus-go` does not ship a `Setpoint` feature client
  yet, so it is left for a later, separate change.

## [0.3.3-dev] - 2026-07-26

### Fixed
- **SHIP transport logs no longer pollute the NDJSON stream in `-json` mode.**
  Since the first release, `eebus-bridge` has been emitting `WARN ndjson:
  skipping unparseable line` on lines like `DEBUG Send: d:_i:...` and
  `DEBUG Recv: d:_i:...`. These are raw SHIP/WebSocket frame dumps produced by
  `ship-go` (`websocket.go` `Trace("Send:"/"Recv:", ski, text)`) via the logger
  passed to `service.SetLogging`.
  The bug: that logger was created with `internal.NewLogger(logLevel)` which
  defaults to `os.Stdout`, and was never redirected to stderr — unlike `AppLog`
  and the scanner's `logOut`, which both honor `-json` mode. In `-json` mode,
  stdout is reserved for the NDJSON data stream consumed by the bridge, so any
  non-JSON line on it triggers the warning.
  The fix is a one-line `logger.SetWriter(logWriter)` in `main.go` (same
  destination already used by `AppLog` and `scanner.logOut`). No behavior
  change, no data loss: the bridge was already filtering these lines correctly,
  this just stops emitting them on the wrong stream in the first place.

### Notes
- This also closes a minor information-disclosure vector: the `Send:`/`Recv:`
  traces dump the SKI and raw message fragments, which should not land in the
  captured add-on log stream even when harmless. Now they go to stderr
  alongside all other diagnostic output.

## [0.3.2-dev] - 2026-07-26

### Fixed
- **Backfill measurement values the broad read does not return.** Diagnostic
  logs from the Saunier Duval VR920 revealed the real culprit behind the
  "missing metrics": the device declares 16 measurements on its
  HeatPumpAppliance entity, but a broad `RequestData(nil, nil)` only returns
  ONE value (the last one that changed). The other 15 declared measurements
  were therefore never cached and never published, even though the device
  genuinely exposes them over SPINE.
  The fix adds `backfillMissingMeasurements`: after the broad read, the
  scanner iterates over the declared descriptions and, for every
  `MeasurementId` whose value is NOT already in the cache, issues a targeted
  `RequestData` with a selector pinned to that id. Each missing id is
  requested at most once per poll cycle (no retry storm). Responses arrive
  asynchronously via `DataChange` events and are rendered by the existing
  `RenderEntityData` path (no amplification loop: DataChange never re-pulls).
  Cheap when the broad read already returned everything (no missing ids =>
  no extra requests); necessary when it did not.

### Notes
- Debug log line added: `entity X: backfilled N missing measurement ids (of M
  declared)` so the operator can confirm the backfill is running and how many
  ids it had to chase.
- `0.3.1-dev` diagnostic logging (`HandleEvent` entity addr+type, description
  list dump) is retained — keep `log_level: debug` if you want to verify.

## [0.3.1-dev] - 2026-07-26

### Added
- **Diagnostic logging for "missing metrics" reports.** Two improvements to
  make it possible to tell, from the add-on log alone, whether a metric the
  device's own app shows is actually exposed over SPINE:
  - `HandleEvent` now logs the entity address and entity type on every SPINE
    event (previously only `entity=<bool>`, which hid WHICH entity a
    `measurementListData` / `measurementDescriptionListData` arrived on).
  - `renderMeasurementsJSON` now logs every description the device declares
    (`id=X type=... commodity=... scope=... unit=...`) at debug level, not just
    the count. This shows exactly what the device exposes over SPINE — even
    measurements that have no value yet, or that we are not rendering.

### Notes
- No behavior change: the same measurements are still published. This release
  only adds observability so we can determine, on the VR920, whether the
  missing humidity / water-pressure / heating-curve metrics are never sent by
  the device over SPINE (in which case the vendor app is using a different
  channel) or are sent but dropped somewhere in our pipeline.

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

[Unreleased]: https://github.com/tbazire/homeassistant-addons/compare/dev-v0.5.1...HEAD
[0.5.1-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.5.1
[0.5.0-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.5.0
[0.4.0-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.4.0
[0.3.3-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.3.3
[0.3.2-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.3.2
[0.3.1-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.3.1
[0.3.0-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.3.0
[0.2.0-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.2.0
[0.1.0-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.1.0
