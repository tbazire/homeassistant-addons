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

## [0.6.6-dev] - 2026-08-02

Fixes the LPC consumption-limit sensor staying frozen on its last value after
the limit is cleared, and standardises the "0 = no active limit" semantic
across the slider and the sensor so both surfaces agree after a clear.

### Root cause

Setting the LPC limit to 0 on the HA slider (or any value ≤ 0) clears the
limit on the device (`Dispatch` → `WriteConsumptionLimit(IsActive:false)`).
But `forwardSignal` only emitted the `consumption_limit` signal when
`IsActive == true`. After a clear, the device notifies `IsActive == false`,
the daemon emitted **no** `uc_signal` line, the bridge republished nothing,
and the sensor kept its last active value indefinitely — the slider and the
sensor then disagreed, with no way for the user to tell the limit was gone.

### Fixed

- **`consumption_limit` sensor now refreshes to 0 after a clear.**
  `forwardSignal` (`DataUpdateLimit` case) now always emits a value — 0 when
  the limit is inactive, the actual watts value when active — instead of
  staying silent on the inactive transition. The bridge already published
  state unconditionally for `uc_signal` lines, so the only fix needed was on
  the daemon side.
- **Slider and sensor now agree.** `EntityState` returns `"0"` for an inactive
  limit (previously `""`, which HA rendered as "unknown"), matching the
  `consumption_limit` sensor's 0. After a clear, both the number entity and
  the sensor read 0.

### Changed

- **"0 = no active limit" is now the documented wire semantic**, consistent
  across all three LPC surfaces: `Dispatch` (set value ≤ 0 → clear),
  `EntityState` (inactive → "0"), and `forwardSignal` (inactive → emits 0).
  To disable the LPC limit, set the slider to 0.

### Tests

- `lpc_test.go`: `TestZeroMeansClearIsTheWireSemantic` pins the shared
  rendering path (`formatWatts(0) == "0"`) that all three surfaces rely on,
  so a future refactor cannot silently desync the slider/sensor again.

### Notes

- No bridge change, no wire-contract change, no config change — daemon-only
  behaviour fix. Existing pairings pick it up on add-on restart.
- This does not add a dedicated "clear" button entity: 0 on the slider is the
  clear action. A separate button is out of scope (can be added later if the
  0=clear mapping proves ambiguous in practice).

## [0.6.5-dev] - 2026-08-01

Fixes a two-sided bug that left the OHPCF climate entity's state (the
compressor's action: `heating` / `idle` / `off`) frozen at its discovery value
forever. After the initial announcement the climate entity never tracked the
device again, even when the compressor started, paused or stopped.

### Root cause

Two independent defects, one in each binary, both needed to reproduce:

1. **Daemon (`eebusd`)** — the OHPCF module routed only `UseCaseSupportUpdate`
   (device announces/revokes OHPCF support) to the controllable-emission path.
   The compressor's runtime state transitions arrive as
   `DataUpdateConsumptionState` events, which were swallowed by a deliberate
   no-op in `forwardSignal` on the assumption that "the state is already
   reflected on the climate entity's action topic". Nothing reflected it: the
   event was received and dropped. `UseCaseSupportUpdate` is change-gated by
   `slices.Compare` in eebus-go and fires only at announce/revoke, never on a
   state transition, so the controllable line was never re-emitted.

2. **Bridge** — `OnControllable` *did* compute a correct refresh payload
   (`StateTopic`/`StateValue`/`ActionTopic`/`ActionValue`, with
   `Config == nil` to avoid re-publishing discovery) on subsequent calls. But
   the orchestrator guarded every `publishState` call for controllables inside
   `if disc.Config != nil`, so refresh payloads were computed and then
   silently dropped — unlike `measurement` and `uc_signal`, which publish
   state unconditionally.

The underlying SPINE layer (spine-go) confirmed correct: `processNotify`/
`processReply`/`processWrite` publish a data-change event on every received
message, and OHPCF fires `DataUpdateConsumptionState` whenever the payload
carries a non-nil `PowerSequence.State` — i.e. on every real transition.

### Fixed

- **OHPCF climate entity now tracks the compressor in real time.** The OHPCF
  module routes `DataUpdateConsumptionState` to the controllable-emission
  callback (the same path as `UseCaseSupportUpdate`), so a state transition
  re-runs `EntityState` → `emitControllable` and the bridge republishes the
  climate entity's `action/state` (and `mode/state`) topics.
- **Per-entity state deduplication** (`recordStateTransition`) suppresses
  redundant refreshes: spine-go does not diff, so it re-fires
  `DataUpdateConsumptionState` on every notify that carries a `State` — even
  when the value is unchanged. The module now caches the last mapped action
  per `(ski, entity)` and emits only on a genuine change, preventing an MQTT
  refresh storm from periodic device notifies. The dedup is "changed since
  last publish", so a `running → paused → running` cycle emits all three.
- **Bridge republishes controllable state on every line, not just discovery.**
  `publishState(State)` and `publishState(Action)` are now called outside the
  `if disc.Config != nil` guard in the orchestrator, mirroring how
  `measurement` and `uc_signal` already behave. Discovery + command-topic
  subscription remain gated (first announcement only).

### Changed

- `ohpcf.Bind` callback switch: `DataUpdateConsumptionState` is now an
  explicit case routed to `cbs.Event` (was a no-op reached only via
  `forwardSignal`). The `forwardSignal` switch still lists it for
  exhaustiveness but it is no longer reachable from the bind path.
- `Module` gains a `lastState map[string]string` cache guarded by a mutex
  (the bind callback may fire from several SPINE reader goroutines).

### Tests

- `ohpcf_test.go`: `TestRecordStateTransition_Dedup` (first emit, identical
  suppressed, transition emits, back-transition emits),
  `TestRecordStateTransition_EmptyIgnored` (empty/unmapped state never
  clears the cache or blanks the entity), `TestRecordStateTransition_Per
  EntityIsolation` (two compressors dedup independently),
  `TestEntityStateKeyFormat` (nil-entity fallback).
- `discovery_climate_test.go`:
  `TestOnControllable_Climate_StateRefreshUpdatesAction` — pins the mapper
  half of the fix: a second `OnControllable` call with a different `State`
  must return updated `ActionValue`/`StateValue` without re-publishing
  discovery (`running` → `idle` → `off`).

### Notes

- This is a behaviour fix only; no wire-contract or config change. Existing
  pairings pick it up on add-on restart.
- The LPC `number` entity already refreshed correctly (its state flows
  through the same orchestrator path, now fixed) — no change for LPC users.

## [0.6.4-dev] - 2026-08-01

Corrects the 0.6.2-dev fix for the LPC slider cap: the original approach relied
on leaving the HA `number` without a `max`, expecting HA to treat it as
unbounded. That assumption was **wrong** — Home Assistant always applies a max
to a number entity (hard-coded default of 100 when omitted, per the
[MQTT number docs](https://www.home-assistant.io/integrations/number.mqtt/)).
So on devices that do not expose a nominal max (the VR920), the slider stayed
capped at 100 W despite 0.6.2-dev.

This release publishes an explicit, configurable fallback max for exactly that
case, so the slider reflects a realistic residential ceiling instead of 100.

### Fixed

- **LPC slider no longer capped at 100 W when the device exposes no nominal
  max.** When a `number` use case returns no device-derived range, the daemon
  now applies a fallback range `{min: 0, max: <fallback>, step: 1}`. The
  fallback max defaults to **25000 W** (covers residential heat pumps,
  single/three-phase wallboxes, inverters and batteries) and is configurable
  via the new `write.lpc_max_limit_w` add-on option. The device's SPINE layer
  remains the final authority and rejects out-of-range values with a clean
  `command_result`. A device-derived nominal max always wins over the fallback.

### Added

- **Add-on option `write.lpc_max_limit_w`** (default `0` = use the built-in
  25000 W default). Lets the operator raise/lower the slider ceiling for
  atypical hardware (e.g. a commercial 50 kW wallbox), flowing through the
  bridge (`EEBUS_WRITE_LPC_MAX_LIMIT_W`) to eebusd's
  `-write-lpc-max-limit-w` flag.
- **`Config.EffectiveLPCMaxLimit()`** resolves the configured-or-default max
  in one place (shared by the daemon and tests).
- **`App.applyNumberRangeFallback(component, rng)`** centralizes the
  fallback rule, extracted from `onWriteUseCaseEvent` so it is unit-testable
  without a populated registry. Non-number components (climate/switch/select)
  are unaffected.

### Notes

- This supersedes the 0.6.2-dev "unbounded when nominal_max absent" behavior,
  which was based on an incorrect assumption about HA's defaults. The
  `range` wire-contract field added in 0.6.2-dev is reused unchanged.
- 25000 W was chosen as a generous residential ceiling: the slider is a hint,
  not a hard validation — the device enforces its real limits via SPINE.

## [0.6.3-dev] - 2026-08-01

Fixes a stdout write race that produced corrupt NDJSON lines on the bridge
side. Under realistic load (multiple LPC read signals emitted in a burst when a
device becomes compatible), the bridge logged:

    WARN ndjson: skipping unparseable line
    err="invalid character '{' after top-level value"

and dropped the affected `uc_signal` line — which is exactly why some LPC
sensors occasionally failed to appear.

### Fixed

- **NDJSON lines no longer interleave under concurrent emission.**
  `emitSignal` and `emitControllable` each performed two separate
  `os.Stdout.Write` calls (payload, then `"\n"`) with no synchronization. SPINE
  data-update callbacks fire from multiple goroutines, so the bytes of two
  lines could interleave and merge into a single `"{...}{...}"` line that the
  bridge's NDJSON parser rejects. A dedicated `outMu` mutex now serializes
  every line, and a single `writeLine` helper writes the payload + newline in
  one `Write` call — the atomic-write guarantee that prevents the split.

### Changed

- **`App` gained an injectable `out io.Writer`** (defaults to `os.Stdout` in
  `NewApp`). `writeLine` writes through it, so the atomicity contract is now
  unit-testable without touching the real stdout. No behavior change for
  production (stdout remains the sink).

### Notes

- The read-side scanner (`scanner/export.go writeJSON`) was already a single
  `Write(append(b, '\n'))` call per line and was not affected; the race was
  isolated to the write-side emitters in `app.go`.
- Regression tests added: `TestWriteLineAtomicUnderConcurrency` (32 goroutines
  × 50 lines, asserts every line is well-formed) and
  `TestWriteLineSingleCallPerLine` (one Write per line). Both pass under
  `-race`.

## [0.6.2-dev] - 2026-08-01

Follow-up to 0.6.1-dev: the LPC `number` entity (the power-limit setpoint in
watts) was silently capped at **100 W** because it was published without a
`max`, and Home Assistant falls back to `max=100` whenever a number entity omits
one. This made legitimate limits (a heat pump easily draws thousands of watts)
impossible to set from the HA UI. The wire contract now carries an optional
input range so the slider reflects the device's real capability — or is left
unbounded when the device does not advertise a ceiling.

### Fixed

- **LPC power-limit slider is no longer capped at 100 W.** The `number` entity
  advertised no `min`/`max`/`step`, so HA applied its built-in default
  (`max=100`). The `controllable` NDJSON line now carries an optional `range`
  object (`{min, max?, step}`); the bridge propagates it to the HA number
  payload. When the device exposes a nominal maximum consumption (LPC Scenario
  4), the slider max is set to that value; otherwise the number is published
  without a max (free-form input) and the device itself rejects out-of-range
  values via SPINE. This is the case on the VR920, which does not expose the
  `ElectricalConnection` characteristic for LPC.

### Changed

- **Wire contract: `controllable` gained an optional `range` field**
  (eebusd → bridge). It is a nested object `{min, max?, step}`; `max` is itself
  optional within the range (absent ⇒ unbounded number). The field is omitted
  entirely for non-number components and for number use cases that return no
  range — so the change is fully backward compatible: an older bridge ignores
  the unknown field, and an older eebusd simply does not emit it.
- **`WriteUseCase` interface gained `NumberRangeForEntity(entity)`** returning
  `*NumberRange` (nil when the use case does not constrain its input). LPC
  derives the max from the device's nominal max; OHPCF returns nil (climate
  entity, not a numeric setpoint). Future numeric use cases (LPP, OPEV) will
  reuse the same path.

### Notes

- On devices that DO expose `nominal_max`, the slider is now bounded to the
  device's rated consumption ceiling. On devices that do not (VR920 today), the
  number is unbounded — the operator can enter any value and the device's SPINE
  layer is the final authority on what it accepts.
- No new configuration option: the range is derived from the device's own
  advertisement, keeping the add-on zero-config per the generic-first rule.
- The OHPCF `schedule` "data not available" behavior is unchanged and expected
  (the device advertises no power sequence when no flexible consumption process
  is available).

## [0.6.1-dev] - 2026-08-01

Follow-up to 0.6.0-dev: the LPC use case now exposes its **read signals** as
Home Assistant sensors, reusing the `uc_signal` plumbing introduced for OHPCF.
Until now LPC only exposed the `number` control entity (the consumption-limit
setpoint in W); the four observability values the Energy Guard reads back were
queried by eebus-go but never forwarded. They now appear as typed sensors
attached to the same device + entity, with **no bridge change required** — the
generic `OnUcSignal` discovery (value-type + unit driven) added in 0.6.0-dev
handles them directly.

### Added

- **LPC now exposes 4 sensors** per compatible entity:
  - `consumption_limit` → sensor `power` (W, state_class measurement) — only
    emitted when a limit is currently active (SPINE `IsActive=true`).
  - `failsafe_power_limit` → sensor `power` (W) — the failsafe active-power
    cap that takes over in init/failsafe state.
  - `nominal_max` → sensor `power` (W) — the device's contractual/rated
    consumption ceiling (CEM → contractual nominal max; other entities →
    power-consumption nominal max).
  - `failsafe_duration_min` → sensor `duration` (min; wire seconds are
    converted to minutes) — the minimum time the device stays in failsafe.
  The sensors share the HA device of the `number` control entity.

### Changed

- The LPC module's `Bind`/`EmitSignals` are no longer no-ops (the placeholder
  noted in 0.6.0-dev's "Architecture" section). `Bind` forwards the four LPC
  data-update events; `EmitSignals` publishes the initial snapshot when a
  device becomes compatible, so HA does not show "unknown" before the first
  device notification.
- The `TestFormatWatts` regression table was kept (it pins the wire contract
  shared by the initial state publish and the number-signal value formatting);
  an `EmitSignals` nil-safe guard test was added.

### Notes

- No command surface was added: this lot is read-only and does not change
  `write.enable` semantics. The LPC `set`/`clear` write actions are unchanged.
- The bridge discovery remains generic: the same `signalSensorAttrs` path
  serves OHPCF and LPC (and any future use case) purely from the value type
  and unit, with no per-use-case signal names.

## [0.6.0-dev] - 2026-07-30

This release exposes the **OHPCF read signals** as Home Assistant sensors. Until
now OHPCF only exposed the `climate` control entity (schedule/pause/resume/
abort); the eight observability values the heat-pump compressor advertises
(requested/max power, start time, pausable/stoppable, minimal run/pause
durations, availability) were queried by eebus-go but never forwarded to the
bridge. They now appear as typed sensors attached to the same device + entity.

### Added

- **New NDJSON kind `uc_signal`** (eebusd → bridge, read-only, backward
  compatible — the parser ignores unknown kinds). One line per signal value,
  carrying `(ski, entity, usecase, signal, value, value_type, unit)`. There is
  no command surface: a `uc_signal` line never triggers a write to eebusd.
- **Use-case read-signal plumbing.** `WriteUseCase` gained a `SignalCallback`
  (via a `Callbacks` struct handed to `Bind`) and an `EmitSignals(ski, entity)`
  method. A use case forwards its read values on two paths: live (on each
  upstream data-update event) and initial snapshot (when a device becomes
  compatible, so HA does not show "unknown" before the first notification).
- **OHPCF now exposes 8 sensors** per compatible compressor entity:
  - `requested_power` / `max_power` → sensor `power` (W, state_class measurement)
  - `start_time` → sensor `timestamp`
  - `min_run_duration` / `min_pause_duration` → sensor `duration` (min; wire
    seconds are converted to minutes)
  - `is_pausable` / `is_stoppable` → binary_sensor (payload true/false)
  - `is_available` (optional) → binary_sensor
  The sensors share the HA device of the `climate` control entity.
- **Bridge `OnUcSignal` discovery** chooses the HA component (sensor vs
  binary_sensor) and device_class from the value type, so the same path serves
  any future use case's signals (LPC nominal-max, etc.) without per-use-case
  logic.

### Architecture

- The signal plumbing is generic: LPC (and future use cases) already implement
  the new `Bind`/`EmitSignals` signatures as no-ops and will light up sensors
  the moment they start emitting signals — no bridge change required.
- `WriteUseCase` callbacks are bundled in a `Callbacks` struct (Event + Signal)
  rather than individual args, keeping `Bind` readable.

### Notes

- The eight OHPCF read signals are surfaced as recommended in the OHPCF audit;
  the deeper SPINE data (multi-slot schedules, ScheduleConstraints, energy/
  probabilistic values) is not wrapped by eebus-go's OHPCF and remains out of
  scope.
- No command surface was added: this lot is read-only and does not change
  `write.enable` semantics.

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

[Unreleased]: https://github.com/tbazire/homeassistant-addons/compare/dev-v0.6.4...HEAD
[0.6.4-dev]: https://github.com/tbazire/homeassistant-addons/compare/dev-v0.6.3...dev-v0.6.4
[0.6.3-dev]: https://github.com/tbazire/homeassistant-addons/compare/dev-v0.6.2...dev-v0.6.3
[0.6.2-dev]: https://github.com/tbazire/homeassistant-addons/compare/dev-v0.6.1...dev-v0.6.2
[0.6.1-dev]: https://github.com/tbazire/homeassistant-addons/compare/dev-v0.6.0...dev-v0.6.1
[0.6.0-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.6.0
[0.5.1-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.5.1
[0.5.0-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.5.0
[0.4.0-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.4.0
[0.3.3-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.3.3
[0.3.2-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.3.2
[0.3.1-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.3.1
[0.3.0-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.3.0
[0.2.0-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.2.0
[0.1.0-dev]: https://github.com/tbazire/homeassistant-addons/releases/tag/dev-v0.1.0
