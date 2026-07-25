<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- SPDX-FileCopyrightText: 2026 Tommy Bazire -->

# EEBUS Bridge (Dev)

[![Channel](https://img.shields.io/badge/channel-dev-orange.svg)](./config.yaml)
[![Version](https://img.shields.io/badge/version-0.1.0--dev-41BDF5.svg)](./config.yaml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](../LICENSE)

> ⚠️ **Development channel.** This add-on ships the same Go code as the
> production **[EEBUS Bridge](../eebus_bridge)**, but on a separate slug and
> image so users can preview upcoming changes. It is **mutually exclusive**
> with the production add-on (see below).

A generic EEBUS bridge for Home Assistant. It pairs with **any** EEBUS-capable
device on your local network — heat pumps, wallboxes, inverters, energy
managers — and exposes its measurements as native Home Assistant sensors via
**MQTT discovery**. No vendor-specific code, no hardcoded device IDs.

> Works with: Saunier Duval/Vaillant VR920, and any device implementing the EEBUS SHIP/SPINE standard.

## ⚠️ Mutually exclusive with the production add-on

The dev and production add-ons share the same internal identifiers by design:

- the **SHIP TCP port** `4711`,
- the **MQTT ClientID** `eebus-bridge`,
- the **HA discovery topic** namespace `homeassistant/sensor/eebus_bridge/#`,
- the default **MQTT state prefix** `eebus`.

If both add-ons run at the same time against the same broker and the same
EEBUS device, they will fight for the SHIP connection, kick each other off
the MQTT broker (duplicate ClientID), and overwrite each other's discovery
messages. **Only one of them can be active at a time.** Stop the production
add-on before starting this one (and vice-versa).

## How it works

```
┌───────────────┐   SHIP/SPINE    ┌──────────────────┐   MQTT    ┌──────────────┐
│ EEBUS device  │ ◄─────────────► │ eebus_bridge_dev │ ────────► │ Home Assistant
│ (heat pump…)  │   mDNS + TLS    │  (add-on, dev)   │  discovery│  (sensors)   │
└───────────────┘                 └──────────────────┘           └──────────────┘
```

1. `eebusd` announces itself as a CEM (Customer Energy Management System) on
   the network via mDNS.
2. It pairs with the EEBUS device(s) you authorize (or auto-discovers them).
3. It reads measurements, device info, configuration and diagnosis over SPINE.
4. `eebus-bridge` translates that into MQTT discovery messages.
5. Home Assistant automatically creates one **device** per EEBUS gateway, with
   one **sensor** per measurement (power, energy, current, voltage, …).

## Prerequisites

- A Home Assistant instance with the **Mosquitto broker** add-on (or any MQTT
  broker reachable from HA).
- An EEBUS-capable device on the **same LAN** as Home Assistant.
- The device must allow pairing with a new CEM (some devices limit the number
  of simultaneous pairings — consult its manual).
- The **production** EEBUS Bridge add-on must be **stopped** (see above).

## Installation

1. Add this repository to Home Assistant:
   **Settings → Add-ons → Add-on Store → ⋯ → Repositories** →
   `https://github.com/tbazire/homeassistant-addons`.
2. Find **EEBUS Bridge (Dev)** in the store and click **Install**.
3. Configure the add-on (see below).
4. Start it. The first start generates a SHIP certificate and persists it in
   `/data`; this certificate **must** survive restarts (keep backups enabled).

## Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `log_level` | enum | `info` | Log verbosity: `trace`, `debug`, `info`, `warning`, `error`. |
| `poll_interval` | int (seconds) | `60` | How often `eebusd` proactively re-reads each entity. `0` = subscription-only. |
| `pairing.auto_accept` | bool | `false` | Auto-trust any incoming pairing request. Insecure — enable only on a trusted network during first pairing. |
| `pairing.remote_ski` | hex (40 chars) | `""` | SKI of a specific device to pair with. Leave empty to rely on auto-discovery. |
| `pairing.secret` | password | `""` | Optional SHIP pairing secret (hex), enables listener pairing mode. |
| `eebusd.brand` | string | `EEBusBridge` | mDNS brand name announced by the add-on. |
| `eebusd.model` | string | `Bridge-1` | mDNS model name. |
| `eebusd.serial` | string | `bridge-0001` | Serial number. **Must be unique on the network.** |
| `eebusd.vendor` | string | `EBRG` | EEBUS vendor code (3+ characters). |
| `eebusd.port` | port | `4711` | Local TCP port for inbound SHIP connections. |
| `mqtt.prefix` | string | `eebus` | MQTT prefix for state topics. |
| `mqtt.discovery_prefix` | string | `homeassistant` | HA MQTT discovery prefix. |

The MQTT broker is resolved automatically from the Home Assistant Supervisor
(the Mosquitto add-on). You do not need to set a broker address unless you use
an external broker.

### First pairing

Most EEBUS devices require you to **approve** the new CEM on their side. The
typical procedure:

1. Start the add-on with `pairing.auto_accept = true` (only during pairing).
2. On the device's own UI, start "add a new energy manager" / "scan for CEM".
3. The device discovers `eebusd` via mDNS and asks you to confirm.
4. Confirm on the device. Pairing completes in a few seconds.
5. You can now set `pairing.auto_accept = false` and restart the add-on.

If you change `eebusd.serial` or delete the persistent `/data/eebus`
certificate, the device will see a *different* CEM and you will have to
re-pair. Keep backups.

## Security

- **No secrets in the image or repository.** All credentials are injected by
  Home Assistant at runtime.
- **MQTT credentials** are resolved from the Supervisor (Mosquitto add-on) by
  default; never logged.
- **SHIP private key** is generated in the container at first start, stored in
  HA's private `/data`, mode `0600`, never logged.
- **Minimal permissions**: the only non-default permission is
  `host_network: true`, which is required by the EEBUS protocol (mDNS multicast
  + inbound SHIP TCP). See [`../SECURITY.md`](../SECURITY.md) for the full
  threat model.
- **Container images are signed with Cosign** — verify with:
  ```
  cosign verify ghcr.io/tbazire/eebus-bridge-dev:0.1.0-dev
  ```

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| No device discovered | mDNS blocked / wrong network | Check `log_level: debug`; ensure the device and HA are on the same L2 network; allow UDP 5353. |
| Pairing fails / denied | Device already has its max CEM count | Remove an old CEM on the device's UI, then retry. |
| Pairing lost after restart | Persistent `/data` was wiped | Restore from backup, or re-pair (a new cert was generated). |
| No sensors in HA | MQTT broker not available | Install the Mosquitto add-on, or set a broker in the add-on config. |
| MQTT client disconnected | Prod add-on is also running | Stop the production EEBUS Bridge add-on (mutually exclusive — see above). |
| Measurements stuck | Device is pull-only and `poll_interval = 0` | Set `poll_interval` to e.g. `60`. |

For deeper diagnostics see [`DOCS.md`](./DOCS.md).

## Reporting bugs

This channel exists precisely to collect feedback on upcoming changes. If you
hit a bug or an unexpected behavior, please open an issue at
<https://github.com/tbazire/homeassistant-addons/issues> and mention that you
are running the **dev** channel (slug `eebus_bridge_dev`).

## License

Apache License 2.0 — see [`../LICENSE`](../LICENSE).
