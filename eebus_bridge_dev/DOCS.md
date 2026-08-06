<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- SPDX-FileCopyrightText: 2026 Tommy Bazire -->

# EEBUS Bridge (Dev) — Technical documentation

This document is for integrators and contributors. For end-user instructions
see [`README.md`](./README.md).

## Architecture

The add-on is composed of two Go binaries running in one container, supervised
by s6-overlay v3:

```
┌───────────────────────────────────────────────────────────────┐
│ Container (HA add-on, s6-overlay v3, ENTRYPOINT /init)         │
│                                                                │
│   run.sh  ──► exec eebus-bridge                                │
│                       │                                        │
│                       │ spawns subprocess                      │
│                       ▼                                        │
│                ┌─────────────┐   stdout (NDJSON)               │
│                │  eebusd     │ ────────────► eebus-bridge      │
│                │  EEBUS core │   stderr (logs) ─► HA logs      │
│                └─────────────┘                                 │
│                       ▲                                        │
│                       │ mDNS (UDP 5353) + SHIP TCP (4711)      │
└───────────────────────┼────────────────────────────────────────┘
                        │
                  EEBUS device (LAN)
```

| Binary | Responsibility | Code |
|--------|----------------|------|
| `eebusd` | EEBUS-pure daemon. mDNS announcement, SHIP pairing, SPINE scan, periodic pulls, NDJSON export on stdout. | [`eebusd/`](./eebusd) |
| `eebus-bridge` | Non-EEBUS consumer. Subprocess management of `eebusd`, NDJSON parsing, MQTT client, HA discovery mapping. | [`bridge/`](./bridge) |

The split is deliberate:

- `eebusd` has **zero** knowledge of MQTT or Home Assistant. It can be used
  standalone (`./eebusd -json`) and piped to anything.
- `eebus-bridge` has **zero** dependency on the EEBUS libraries. It is a plain
  Unix consumer (subprocess + pipe). This keeps the two concerns decoupled and
  independently testable.

## NDJSON wire contract

When `eebusd` runs with `-json`, every line on stdout is one self-contained
JSON object. The `kind` field discriminates the payload:

```jsonc
{"kind":"device",        "ski":"…", "entity":"0",   "time":"…", "entity_type":"DeviceInformation"}
{"kind":"manufacturer",  "ski":"…", "entity":"0",   "time":"…", "brand_name":"Saunier Duval", "device_name":"GeniaAir Mono", "serial":"…", "sw_version":"…"}
{"kind":"configuration", "ski":"…", "entity":"0",   "time":"…", "key_id":"5", "key_name":"Heartbeat", "value":"300", "value_type":"integer"}
{"kind":"measurement",   "ski":"…", "entity":"3.1", "time":"…", "id":"5", "type":"Power", "commodity":"Electricity", "scope":"AC-Output", "unit":"W", "value":1234.5}
{"kind":"diagnosis",     "ski":"…", "entity":"0",   "time":"…", "operating_state":"normalOperation", "up_time":"PT1H"}
```

Logs go to **stderr** in `-json` mode (so they never corrupt the stream). In
the default text mode, logs and tables both go to stdout.

The `ski` field lets the bridge group every entity of one physical device under
a single Home Assistant **device**.

## MQTT topics

```
Discovery   homeassistant/sensor/eebus_bridge/<unique_id>/config   (retained)
State       <mqtt.prefix>/<ski>/<entity>/<id>/state
```

`unique_id` is stable and derived from `ski + entity + measurement_id`, so a
device reconnect does not create duplicate sensors.

## Lifecycle

### Start
1. HA Supervisor starts the container.
2. s6-overlay runs `run.sh`.
3. `run.sh` reads options via `bashio`, resolves the MQTT broker
   (Supervisor service or user override), exports `EEBUS_*` env vars.
4. `run.sh` execs `eebus-bridge`.
5. `eebus-bridge` connects to MQTT, then spawns `eebusd` as a subprocess.
6. `eebusd` loads (or generates) its certificate, announces on mDNS, and
   begins pairing / scanning.

### Stop (SIGTERM from HA)
1. `eebus-bridge` receives SIGTERM, propagates it to the `eebusd` subprocess.
2. `eebusd` tears down the SHIP hub (including mDNS announcement — important,
   otherwise the device keeps trying to reach a ghost).
3. `eebus-bridge` disconnects from MQTT cleanly (LWT retained = offline).
4. s6-overlay reaps the processes.

### Crash of `eebusd`
1. `eebus-bridge` detects subprocess exit, restarts `eebusd` (up to 3 times
   with backoff).
2. After 3 failures, `eebus-bridge` exits so s6-overlay restarts the whole
   add-on.

## Update / backup

- **Backup**: HA's add-on backup captures `/data`, which contains the SHIP
  certificate and ring buffer. **Keep backups enabled** — restoring them is
  the only way to keep pairings after a reinstall.
- **Update**: install the new version. The certificate in `/data` is reused;
  pairings survive.
- **Restore**: HA restores `/data`. Pairings survive.

## Diagnostics

| Symptom | Where to look |
|---------|---------------|
| Nothing happens at startup | Add-on logs (stderr of `eebusd`). Set `log_level: trace`. |
| Pairing stuck | `pairing state [...]` log lines. The device's own UI may also show the handshake. |
| Measurements not updating | Set `poll_interval: 30`; check the device is online; check MQTT messages with `mosquitto_sub -t 'eebus/#' -v`. |
| HA sensors missing | Confirm discovery messages: `mosquitto_sub -t 'homeassistant/sensor/eebus_bridge/#' -v`. |
| Wrong device name in HA | The `manufacturer` kind provides brand/model. If missing, the device exposes no DeviceClassification server feature. |
| Write command has no effect | `write.enable` must be `true` and the per-use-case toggle (`write.lpc_enabled` / `write.ohpcf_enabled`) too. Confirm the entity appears as a button/number in HA and check the bridge log for `command result status=error`. The device may simply not support the requested action (e.g. a compressor that is not pausable). |

## Write commands (control entities)

By default the add-on is **read-only**: it publishes sensors but never sends
anything to the device. Setting `write.enable: true` opens an opt-in control
channel so Home Assistant can drive the device.

> ⚠️ **Write commands are off by default.** This is deliberate: turning the
> add-on into a controller changes its security profile (it can now act on the
> physical world). Enable it only when you actually want to control the device
> from HA, and on a trusted network.

### How it works

```
HA UI ──► MQTT command_topic ──► eebus-bridge ──► stdin NDJSON ──► eebusd ──► SPINE write ──► device
                                              ◄── command_result ◄──
```

1. `eebusd` registers its write use cases (OHPCF, …) on the local CEM entity
   when `write.enable` is set.
2. When a paired device announces support for one of them via SPINE, `eebusd`
   emits a `controllable` NDJSON line to the bridge.
3. The bridge creates the matching HA entities (buttons for OHPCF, a `number`
   for LPC) and
   subscribes to its `command_topic`s.
4. When the user changes a mode/preset in HA, the bridge encodes the action as
   a `command` NDJSON line and writes it to `eebusd`'s stdin.
5. `eebusd` performs the SPINE write and reports the outcome as a
   `command_result` line, which the bridge logs and publishes on
   `<prefix>/bridge/command_result`.

### Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `write.enable` | bool | `false` | Master switch. When `false`, no write use case is registered, no command topic is subscribed, and the add-on stays strictly read-only. |
| `write.use_cases` | string | `"auto"` | `"auto"` activates every use case the device announces it supports. Set to a comma-separated list (e.g. `"ohpcf"`) to restrict to specific ones. |
| `write.device_profile` | enum | `"auto"` | `auto` = trust the entity types the device advertises (recommended). `heatpump` / `evse` / `inverter` / `battery` / `generic` restrict discovery to that device family — useful when several devices are paired but only one should be controllable. |
| `write.lpc_enabled` | bool | `false` | Per-use-case security toggle for LPC (power consumption limit). Must be `true` for the LPC `number` entity and its sensors to appear. **Opt-in:** `write.enable: true` alone is not enough — see [Per-use-case toggles](#per-use-case-toggles) below. |
| `write.ohpcf_enabled` | bool | `false` | Per-use-case security toggle for OHPCF (heat-pump compressor flexibility). Must be `true` for the OHPCF buttons and their sensors to appear. **Opt-in:** `write.enable: true` alone is not enough — see [Per-use-case toggles](#per-use-case-toggles) below. |
| `write.lpc_max_limit_w` | int | `0` | Fallback upper bound (W) for the LPC power-limit slider when the device does not advertise a nominal max. `0` = built-in default (25000 W). Raise it for atypical hardware (e.g. a commercial wallbox); the device's SPINE layer still rejects genuinely out-of-range values. |

### Per-use-case toggles

Since 0.6.7-dev, `write.enable` is only the **master switch**: it opens the
control channel, but it no longer activates any use case by itself. Each use
case has its own toggle (`write.lpc_enabled`, `write.ohpcf_enabled`), and all
default to `false`. A use case is bound, announced to the device, and exposed
in Home Assistant **only** when its toggle is `true`.

This is deliberate: the two shipped use cases control different physical
quantities (a power limit vs. a heat-pump compressor), and a user who only
wants one should not have the other silently exposed. A disabled use case
leaves no trace — no HA entity, no command topic, no sensor — because the
daemon skips it before it can subscribe to any SPINE event.

> ⚠️ **Upgrading from ≤ 0.6.6-dev:** if you previously set `write.enable: true`,
> your use cases will stop appearing after the update until you also enable the
> corresponding toggle(s). This is an intentional safety change, not a bug.

**Example — control a heat pump only (OHPCF), no power-limit surface:**

```yaml
write:
  enable: true
  ohpcf_enabled: true
  # lpc_enabled stays false (default)
```

**Example — cap power consumption only (LPC), no heat-pump surface:**

```yaml
write:
  enable: true
  lpc_enabled: true
  # ohpcf_enabled stays false (default)
```

**Example — both use cases:**

```yaml
write:
  enable: true
  lpc_enabled: true
  ohpcf_enabled: true
```

### Supported use cases

| Use case | EEBUS name | Typical devices | HA entity | Actions |
|----------|-----------|-----------------|-----------|---------|
| **OHPCF** | Optimization of Self-Consumption by Heat-Pump Compressor Flexibility | Heat pumps (any brand exposing the `SmartEnergyManagementPs` feature: e.g. Saunier Duval/Vaillant VR920, …) | `button` ×4 + `sensor` (process_state) | buttons `schedule` / `pause` / `resume` / `abort` (filtered by capability) |
| **LPC** | Limitation of Power Consumption | Any controllable system exposing `LoadControl` (heat pumps, wallboxes, inverters, batteries, sub-meters) | `number` | power limit in W (set / clear) |
| LPP *(planned)* | Limitation of Power Production | Inverters | `number` | production limit in W |
| OPEV / OSCEV *(planned)* | EV charging control | Wallboxes | `number` / `select` | per-phase current obligation/recommendation |

This list grows as new use case modules ship. Adding a use case is a
self-contained module under `eebusd/internal/writes/<uc>/` that registers
itself at init time — the bridge and dispatcher pick it up automatically, with
no code change outside the module. **The add-on is generic**: it targets any
EBUS-conformant device that advertises the use case, not a specific brand.

### OHPCF example (heat pump)

With `write.enable: true` **and** `write.ohpcf_enabled: true`, pairing a heat
pump that exposes OHPCF produces a set of **button** entities (one per action)
plus a **process_state sensor** per compatible compressor entity:

- **`button.schedule`** → start the optional power consumption process now.
- **`button.pause`** → pause the running compressor *(only if `is_pausable`)*.
- **`button.resume`** → resume a paused compressor *(only if `is_pausable`)*.
- **`button.abort`** → abort the running process *(only if `is_stoppable`)*.

Buttons are momentary triggers: pressing one fires the matching SPINE command
once. They have no state of their own — the process state lives in the sensor
below. The buttons offered are filtered by device capability exactly as the old
climate presets were: `pause`/`resume` appear only when the compressor
advertises `is_pausable`, `abort` only when `is_stoppable`. A button the device
cannot honour is never exposed. If the device rejects a command at run time
(e.g. aborting when no process is active), the bridge log explains the reason
under `command result status=error`.

The compressor's real-time state is exposed as a read-only **`process_state`
sensor**, which carries the raw SPINE enum name:

| State | Meaning |
|-------|---------|
| `available` | No process scheduled |
| `scheduled` | A process is planned but not yet running |
| `running` | The compressor is actively consuming |
| `paused` | The compressor is paused but ready to resume |
| `completed` | The process finished normally |
| `stopped` | The process was aborted |

This is a faithful reflection of the device — unlike the former climate
`action` (`heating`/`idle`/`off`), which conflated several distinct states into
the same value. That mismatch is what motivated the move from `climate` to
buttons + sensor.

In addition, OHPCF exposes **read-only sensors** from the data the compressor
advertises. They attach to the same device and entity as the buttons, so the
whole picture is grouped together:

| Sensor | HA type | Meaning |
|--------|---------|---------|
| `requested_power` | `sensor` (power, W) | Power the process estimates it will consume |
| `max_power` | `sensor` (power, W) | Maximal power value |
| `start_time` | `sensor` (timestamp) | When the scheduled process starts |
| `min_run_duration` | `sensor` (duration, min) | Minimum time a run must last |
| `min_pause_duration` | `sensor` (duration, min) | Minimum time a pause must last |
| `is_pausable` | `binary_sensor` | Whether the CEM may pause the process |
| `is_stoppable` | `binary_sensor` | Whether the CEM may abort the process |

These are informational (no command surface). `is_pausable`/`is_stoppable` also
drive which buttons are offered (see the per-entity action filtering introduced
in 0.5.1-dev).

> ℹ️ **Upgrading from ≤ 0.6.7-dev:** the OHPCF `climate` entity will disappear
> from HA on update, replaced by the buttons + `process_state` sensor. This is
> intentional — the climate representation did not match OHPCF semantics. Any
> automation referencing the climate entity (mode/preset) must be re-pointed at
> the corresponding button.

### LPC example (power consumption limit)

With `write.enable: true` **and** `write.lpc_enabled: true`, pairing any
controllable system that exposes LPC (a heat pump, a wallbox, an inverter, a
battery, …) produces a single `number` entity per compatible entity,
representing the active power consumption limit in watts (W):

- **Set a value** (e.g. `2000`) → cap the device's consumption at 2000 W.
- **Clear the limit** (empty input) → remove the cap; the device returns to
  its normal behaviour.

The entity's state reflects the limit the device last reported. A value of `0`
or a non-positive input is interpreted as "clear" (SPINE LoadControl limits are
absolute magnitudes, so 0 is indistinguishable from "no limit"). If the device
rejects the limit (e.g. below its failsafe floor), the state does not change
and the bridge log explains the reason under `command result status=error`.

> ℹ️ LPC is **generic**: it activates for whatever entity advertises the
> `LoadControl` feature, regardless of brand. The unit (W) is declared by the
> use case module and carried through to HA discovery, so the slider is
> labelled correctly for any device family.

> ℹ️ **Input range.** When the device advertises a nominal maximum consumption
> (LPC Scenario 4), the slider's `max` is set to that value so you cannot
> request more than the hardware can draw. When the device does not expose a
> nominal max (e.g. the Saunier Duval VR920), a **fallback ceiling of 25000 W**
> is applied instead — Home Assistant always enforces a max on a number entity
> (its built-in default is 100), so an explicit realistic value must be
> published to avoid silently capping the slider at 100 W. The fallback is
> configurable via `write.lpc_max_limit_w` (set it to match your hardware; `0`
> keeps the 25000 W default). The device's SPINE layer remains the final
> authority and rejects genuinely out-of-range values with a clean
> `command_result`. In all cases `min` is 0 (a negative power limit has no
> SPINE meaning) and the step is 1 W.

> ℹ️ **Disabling the limit.** Set the slider to **0** to remove the active
> consumption limit. The device clears the cap, and both the number entity and
> the `consumption_limit` sensor read 0 (the wire semantic for "no active
> limit"). Setting any positive value applies a new persistent cap.

In addition to the `number` control entity, LPC exposes **read-only sensors**
from the data the controllable system advertises. They attach to the same device
and entity as the `number` entity:

| Sensor | HA type | Meaning |
|--------|---------|---------|
| `consumption_limit` | `sensor` (power, W) | Currently active consumption limit; reads **0** when no limit is set |
| `failsafe_power_limit` | `sensor` (power, W) | Failsafe active-power cap active in init/failsafe state |
| `nominal_max` | `sensor` (power, W) | Device's contractual / rated consumption ceiling |
| `failsafe_duration_min` | `sensor` (duration, min) | Minimum time the device stays in failsafe state |

These are informational (no command surface) and reuse the same generic
`uc_signal` plumbing as OHPCF: the bridge derives the HA type purely from the
value type and unit, so any future use case's read signals light up without a
bridge change.

## Security review

See [`../SECURITY.md`](../SECURITY.md) for the full threat model and the
list of mandatory rules. In short:

- No secret is ever in the image, the repository, or a log line.
- The daemon runs as a non-root user (`eebus`, uid 911). Only s6-overlay
  `/init` and the privileged startup bits in `run.sh` (chown of `/data/eebus`
  and the bashio reads of `/data/options.json`, which HA writes `0600
  root:root`) run as root; `run.sh` then drops to `eebus` via
  `s6-setuidgid` before exec-ing `eebus-bridge`, so neither the bridge nor its
  `eebusd` child subprocess ever runs as root. (Dev-channel hardening ahead of
  the production add-on.)
- AppArmor is enabled (`apparmor: true`) and the add-on ships a **custom
  profile** ([`apparmor.txt`](./apparmor.txt)) instead of HA's default. The
  single-profile policy grants only the s6-overlay v3 supervision paths, the
  TLS CA bundle (SHIP is TLS-based), TCP/UDP networking (inbound SHIP 4711 +
  outbound MQTT + mDNS 5353), `/data`, and the privilege-drop capabilities
  (`chown`/`setuid`/`setgid`/`dac_override`) needed for the non-root startup
  phase. It deliberately does NOT grant `net_bind_service` (port 4711 > 1024),
  `net_raw`, `sys_admin`, or `ptrace`. The profile is intentionally robust
  (permissive) over maximally strict; it can be tightened to an inner daemon
  subprofile once real AppArmor logs have been observed on a deployed instance.
- The only non-default permission is `host_network: true` (justified by EEBUS).
- Images are Cosign-signed.

## Test plan

See [`tests/test_checklist.md`](./tests/test_checklist.md) for the manual
validation procedure.
