<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- SPDX-FileCopyrightText: 2026 Tommy Bazire -->

# EEBUS Bridge — Technical documentation

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
