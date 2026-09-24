<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- SPDX-FileCopyrightText: 2026 Tommy Bazire -->

# EEBUS Bridge — Test checklist

Manual validation procedure for the **production** add-on. Use this before
cutting a `v*` release tag.

Legend: `[ ]` to do · `[x]` done · `[!]` skipped with justification.

## 1. Static checks (CI-mirrored)

- [ ] `python3 eebus_bridge/tests/test_config_validation.py` passes.
- [ ] `cd eebus_bridge/eebusd && go vet ./... && go test ./...` pass.
- [ ] `cd eebus_bridge/bridge  && go vet ./... && go test ./...` pass.
- [ ] `yamllint -c .yamllint.yaml .` is clean.
- [ ] Every source file has `SPDX-License-Identifier: Apache-2.0`.
- [ ] No file under `certs/`, no `*.key`, `*.crt`, `*.pem`, `.env` is tracked:
      `git status --ignored` shows them as ignored.

## 2. Local Docker build

- [ ] `docker build --build-arg BUILD_FROM=ghcr.io/home-assistant/amd64-base:3.21 -t eebus-bridge:local eebus_bridge/` succeeds.
- [ ] `docker inspect eebus-bridge:local` shows label
      `org.opencontainers.image.licenses=Apache-2.0`.
- [ ] The container starts and reaches the SHIP announcement (log line
      `service started on port 4711 (CEM)` appears).

## 3. Mutually-exclusive with the dev channel

- [ ] The **dev** EEBUS Bridge add-on is **stopped** before starting
      this production add-on (same SHIP port 4711, same MQTT ClientID, same
      discovery topics).
- [ ] Only one of the two add-ons is running at any time.

## 4. Pairing with a real device

Tested device / model: ____________________   Date: __________

- [ ] With `pairing.auto_accept = true`, the device discovers `eebusd`.
- [ ] Confirming on the device's UI completes pairing
      (`pairing COMPLETED with <ski>` in logs).
- [ ] Setting `pairing.auto_accept = false` and restarting does **not** break
      the pairing (cert reused from `/data`).

## 5. MQTT discovery

- [ ] `mosquitto_sub -t 'homeassistant/sensor/eebus_bridge/#' -v` shows one
      discovery message per measurement, retained.
- [ ] One HA **device** appears per EEBUS gateway, with correct brand/model.
- [ ] Each sensor has a stable `unique_id` (survives restart, no duplicates).
- [ ] With no `mqtt.host` set, the broker is auto-discovered from the
      Supervisor and the log shows `MQTT broker auto-discovered from Supervisor`.
- [ ] External broker (issue #40): setting `mqtt.host`/`port`/`user`/
      `password` connects to that broker instead (log shows `Using external
      MQTT broker from configuration`); discovery + states arrive on it.
- [ ] External broker over TLS: `mqtt.ssl: true` + port `8883` connects
      successfully (system CA store).
- [ ] Wrong `mqtt.host` credentials: the add-on retries for ~30s then fails
      with a clear `broker unreachable` error (s6 restarts it).

## 6. State publishing

- [ ] `mosquitto_sub -t 'eebus/#' -v` shows state updates.
- [ ] With `log_level: debug`, the add-on log shows a `mqtt publish` line
      (topic + payload) for every state and discovery message sent, matching
      what `mosquitto_sub` receives.
- [ ] With `log_level: debug`, pressing an OHPCF button shows a `mqtt recv`
      line for the command topic, and `state publish skipped` lines appear
      only for components without state (buttons).
- [ ] Setting `poll_interval = 0` stops proactive pulls but pushed values still
      arrive (if the device pushes).
- [ ] Setting `poll_interval = 30` resumes periodic updates within ~30s.

## 7. HVAC use cases (0.4.0, needs a real HVAC device)

Setup: `write.enable: true` **and** `write.hvac_enabled: true`. The device must
announce the HVAC use cases over SPINE (`DHWCircuit` / `HVACRoom` /
`TemperatureSensor` entities).

- [ ] **Off by default:** with `write.hvac_enabled: false` (or `write.enable:
      false`), pairing the same device produces EXACTLY the same entities as
      0.3.0 — no `water_heater`, no mode select/switch, no room setpoint
      number, and no `mdt/mdsf/mot/mrt/mrhsf` sensors. The generic temperature
      measurement sensors still appear (scanner fallback).
- [ ] **Non-HVAC device unchanged:** with `write.hvac_enabled: true`, pairing a
      device WITHOUT HVAC support (wallbox, VR920 without HVAC features)
      creates no HVAC entity.
- [ ] A `water_heater` entity appears per DHW circuit: current temperature
      (from `mdt`), target temperature with the device's min/max, and the
      supported operation modes.
- [ ] Setting the target temperature publishes on `…/cdt/value/cmd` and the
      device applies it (`command_result status=ok`); the water heater state
      follows on the next refresh.
- [ ] Changing the operation mode publishes on `…/cdsf/mode/cmd` and the mode
      state (fed by `mdsf/operation_mode`) follows.
- [ ] Per HVAC room: a mode `select` (or `switch` when only on/off is
      supported) and a setpoint `number` (°C, device range) appear; both
      control the device and reflect its state.
- [ ] Writing a setpoint while the DHW mode is unknown fails closed with a
      clean `command_result status=error` (never a guessed mode).
- [ ] Unsupported mode writes are rejected by the device with a visible
      `command_result status=error`.
- [ ] **Writes blocked when write off:** reverting `write.enable: false`
      removes every control entity and command topic (read signals included).
- [ ] **Description inventory (on missing sensors):** with `log_level: debug`,
      the `desc id=… type=… scope=… unit=…` lines and the `uc_signal` lines
      (`mdt/mdsf/mot/mrt/mrhsf`) explain which sensors appear — flow/return
      temperature scopes and overrun status values vary per vendor.

## 8. Restart resilience

- [ ] Restarting the add-on keeps all sensors (no duplicates, no orphan).
- [ ] Restarting the add-on keeps the pairing (no re-pair needed).
- [ ] Killing `eebusd` inside the container: the bridge restarts it (≤3x),
      then exits so s6 restarts the add-on.

## 9. Security posture

- [ ] No secret in the logs after a full run with `log_level: trace`.
- [ ] `pairing.secret`, MQTT password are never echoed.
- [ ] `/data/eebus/scanner.key` is mode `0600` inside the container.
- [ ] Daemon runs as non-root: inside the running container
      `ps -o user= -p $(pidof eebus-bridge)` shows `eebus` (not `root`),
      and `id eebus` reports uid 911. `/init` and the `run.sh` startup bits
      legitimately run as root; only the daemon must be non-root.
- [ ] AppArmor custom profile is loaded and active:
      `docker inspect <id> --format '{{.AppArmorProfile}}'` shows
      `eebus_bridge` (the add-on slug = the custom profile name, NOT HA's
      `default`), and `dmesg | grep -i apparmor | grep DENIED` shows no denials
      for the bridge/eebusd binaries during a full pairing + MQTT discovery
      cycle. If DENIED lines appear, the profile in `apparmor.txt` needs the
      corresponding rule (see the comment block at the top of that file).

## 10. Shutdown

- [ ] Stopping the add-on from HA sends SIGTERM, the add-on exits cleanly
      within `timeout` (30s).
- [ ] After stop, mDNS no longer announces the add-on (check with
      `avahi-browse -art | grep -i eebus`).
- [ ] MQTT LWT publishes `offline` (if configured).

## 11. Image signing (release only)

- [ ] `cosign verify ghcr.io/tbazire/eebus-bridge:<version>` succeeds.

## Notes

Use this section for anything noteworthy observed during testing.
