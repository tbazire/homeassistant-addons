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
| Write command has no effect | `write.enable` must be `true`. Confirm the entity appears as a climate/number in HA and check the bridge log for `command result status=error`. The device may simply not support the requested action (e.g. a compressor that is not pausable). |

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
3. The bridge creates the matching HA entity (a `climate` for OHPCF) and
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

### Supported use cases

| Use case | EEBUS name | Typical devices | HA entity | Actions |
|----------|-----------|-----------------|-----------|---------|
| **OHPCF** | Optimization of Self-Consumption by Heat-Pump Compressor Flexibility | Heat pumps (any brand exposing the `SmartEnergyManagementPs` feature: e.g. Saunier Duval/Vaillant VR920, …) | `climate` | modes `off` (abort) / `auto` (schedule); presets `pause` / `resume` |
| **LPC** | Limitation of Power Consumption | Any controllable system exposing `LoadControl` (heat pumps, wallboxes, inverters, batteries, sub-meters) | `number` | power limit in W (set / clear) |
| LPP *(planned)* | Limitation of Power Production | Inverters | `number` | production limit in W |
| OPEV / OSCEV *(planned)* | EV charging control | Wallboxes | `number` / `climate` | per-phase current obligation/recommendation |

This list grows as new use case modules ship. Adding a use case is a
self-contained module under `eebusd/internal/writes/<uc>/` that registers
itself at init time — the bridge and dispatcher pick it up automatically, with
no code change outside the module. **The add-on is generic**: it targets any
EBUS-conformant device that advertises the use case, not a specific brand.

### OHPCF example (heat pump)

With `write.enable: true`, pairing a heat pump that exposes OHPCF produces a
single `climate` entity per compatible compressor entity:

- **Mode `off`** → abort the optional power consumption process.
- **Mode `auto`** → schedule the process (start immediately).
- **Preset `pause`** → pause the running compressor.
- **Preset `resume`** → resume a paused compressor.

The entity's `action` reflects the real compressor state (`heating`, `idle`,
`off`). If the device rejects a command (e.g. the compressor is not pausable),
the action does not change and the bridge log explains the reason under
`command result status=error`.

### LPC example (power consumption limit)

With `write.enable: true`, pairing any controllable system that exposes LPC
(a heat pump, a wallbox, an inverter, a battery, …) produces a single `number`
entity per compatible entity, representing the active power consumption limit
in watts (W):

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

## Security review

See [`../SECURITY.md`](../SECURITY.md) for the full threat model and the
list of mandatory rules. In short:

- No secret is ever in the image, the repository, or a log line.
- The container runs non-root.
- The only non-default permission is `host_network: true` (justified by EEBUS).
- Images are Cosign-signed.

## Test plan

See [`tests/test_checklist.md`](./tests/test_checklist.md) for the manual
validation procedure.
