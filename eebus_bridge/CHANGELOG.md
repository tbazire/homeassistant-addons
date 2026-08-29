<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- SPDX-FileCopyrightText: 2026 Tommy Bazire -->

# Changelog — EEBUS Bridge add-on

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

_Nothing yet._

## [0.3.0] - 2026-08-29

Migration of the dev channel (validated up to `0.9.0-dev` on a real device)
to the production add-on. The add-on is no longer read-only: an opt-in control
channel lets Home Assistant drive compatible devices. Security posture is
raised to match the dev channel (non-root daemon, custom AppArmor profile).

### Added

- **Write/control channel (off by default).** Setting `write.enable: true`
  opens an opt-in control channel so Home Assistant can act on the device.
  Each use case has its own security toggle (`write.lpc_enabled`,
  `write.ohpcf_enabled`, both default `false`) — a use case is bound,
  announced and exposed only when its toggle is on. The dispatcher validates
  device compatibility before every write; command topics are subscribed
  individually (no wildcards).
- **OHPCF use case (heat-pump compressor flexibility).** One HA `button` per
  action (`schedule` / `pause` / `resume` / `abort`, filtered by device
  capability) plus a read-only `process_state` sensor carrying the raw SPINE
  state, and 8 read-only sensors (requested/max power, start time, min run/
  pause durations, is_pausable / is_stoppable, is_available).
- **LPC use case (limitation of power consumption).** A `number` entity (W)
  to cap the consumption of any controllable system exposing `LoadControl`
  (heat pumps, wallboxes, inverters, batteries); 0 clears the limit. The
  slider is bounded by the device's nominal max when advertised, with a
  configurable fallback (`write.lpc_max_limit_w`, default 25000 W). Four
  read-only sensors (consumption_limit, failsafe_power_limit, nominal_max,
  failsafe_duration_min).
- **External MQTT broker support** (`mqtt.host`, `mqtt.port`, `mqtt.user`,
  `mqtt.password`, `mqtt.ssl`), resolving
  [#40](https://github.com/tbazire/homeassistant-addons/issues/40). Setting
  `mqtt.host` connects to that broker instead of the Supervisor-discovered
  one; `mqtt.ssl: true` switches to TLS (`ssl://`, system CA store, typically
  port 8883). Home Assistant's MQTT integration must target the same broker.
- **MQTT traffic logging.** With `log_level: debug` (or `trace`), every
  message exchanged with the broker is logged: outgoing publishes
  (`mqtt publish` with topic, payload, retain), incoming commands
  (`mqtt recv`), subscriptions (`mqtt subscribe`) and deliberately-skipped
  states (`state publish skipped`).
- **New production defaults.** `log_level: warning`, `poll_interval: 30`,
  `eebusd.brand: HomeAssistant`, `eebusd.model: BridgeHA`. Existing installs
  keep their configured values; the defaults apply to fresh installs (or
  after resetting options).

### Security

- **The daemon now runs as a non-root user** (`eebus`, uid/gid 911).
  s6-overlay's `/init` still starts as root (supervision + reading
  `/data/options.json`, written `0600 root:root`), but `run.sh` drops to
  `eebus` via `s6-setuidgid` before exec-ing `eebus-bridge`.
- **Custom AppArmor profile (`apparmor.txt`)** replacing HA's generic
  default. Grants only s6-overlay supervision paths, the TLS CA bundle,
  TCP/UDP networking, `/data`, and the privilege-drop capabilities.
- The repository-root `SECURITY.md` now matches this posture (its
  "non-root, AppArmor default profile" claim predates the migration).

### Fixed

- MQTT subscriptions are re-applied on reconnect (paho does not remember
  them with auto-reconnect enabled; a command topic subscribed before a
  network blip would silently stop firing).
- NDJSON lines can no longer interleave under concurrent emission (single
  atomic write per line, serialized by a mutex) — the root cause of
  occasional `WARN ndjson: skipping unparseable line` and missing sensors.
- The LPC `consumption_limit` sensor refreshes to 0 after the limit is
  cleared (it used to keep its last value indefinitely).
- Write use-case toggles reach the daemon correctly (bool flags are emitted
  as single `-flag=value` tokens).

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

[Unreleased]: https://github.com/tbazire/homeassistant-addons/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/tbazire/homeassistant-addons/releases/tag/v0.3.0
[0.2.0]: https://github.com/tbazire/homeassistant-addons/releases/tag/v0.2.0
[0.1.3-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/v0.1.3-dev
[0.1.2-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/v0.1.2-dev
[0.1.2]: https://github.com/tbazire/homeassistant-addons/releases/tag/v0.1.2
[0.1.1]: https://github.com/tbazire/homeassistant-addons/releases/tag/v0.1.1
[0.1.0]: https://github.com/tbazire/homeassistant-addons/releases/tag/v0.1.0
