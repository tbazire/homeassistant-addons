<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- SPDX-FileCopyrightText: 2026 Tommy Bazire -->

# EEBUS Bridge (Dev) — Test checklist

Manual validation procedure for the **development channel**. Use this before
cutting a `dev-v*` release tag.

Legend: `[ ]` to do · `[x]` done · `[!]` skipped with justification.

## 1. Static checks (CI-mirrored)

- [ ] `python3 eebus_bridge_dev/tests/test_config_validation.py` passes.
- [ ] `cd eebus_bridge_dev/eebusd && go vet ./... && go test ./...` pass.
- [ ] `cd eebus_bridge_dev/bridge  && go vet ./... && go test ./...` pass.
- [ ] `yamllint -c .yamllint.yaml .` is clean.
- [ ] Every source file has `SPDX-License-Identifier: Apache-2.0`.
- [ ] No file under `certs/`, no `*.key`, `*.crt`, `*.pem`, `.env` is tracked:
      `git status --ignored` shows them as ignored.

## 2. Local Docker build

- [ ] `docker build --build-arg BUILD_FROM=ghcr.io/home-assistant/amd64-base:3.21 -t eebus-bridge-dev:local eebus_bridge_dev/` succeeds.
- [ ] `docker inspect eebus-bridge-dev:local` shows label
      `org.opencontainers.image.licenses=Apache-2.0`.
- [ ] The container starts and reaches the SHIP announcement (log line
      `service started on port 4711 (CEM)` appears).

## 3. Mutually-exclusive with production

- [ ] The **production** EEBUS Bridge add-on is **stopped** before starting
      this dev channel (same SHIP port 4711, same MQTT ClientID, same
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

## 6. State publishing

- [ ] `mosquitto_sub -t 'eebus/#' -v` shows state updates.
- [ ] Setting `poll_interval = 0` stops proactive pulls but pushed values still
      arrive (if the device pushes).
- [ ] Setting `poll_interval = 30` resumes periodic updates within ~30s.

## 7. Restart resilience

- [ ] Restarting the add-on keeps all sensors (no duplicates, no orphan).
- [ ] Restarting the add-on keeps the pairing (no re-pair needed).
- [ ] Killing `eebusd` inside the container: the bridge restarts it (≤3x),
      then exits so s6 restarts the add-on.

## 8. Security posture

- [ ] No secret in the logs after a full run with `log_level: trace`.
- [ ] `pairing.secret`, MQTT password are never echoed.
- [ ] `/data/eebus/scanner.key` is mode `0600` inside the container.

## 9. Shutdown

- [ ] Stopping the add-on from HA sends SIGTERM, the add-on exits cleanly
      within `timeout` (30s).
- [ ] After stop, mDNS no longer announces the add-on (check with
      `avahi-browse -art | grep -i eebus`).
- [ ] MQTT LWT publishes `offline` (if configured).

## 10. Image signing (release only)

- [ ] `cosign verify ghcr.io/tbazire/eebus-bridge-dev:<version>` succeeds.

## Notes

Use this section for anything noteworthy observed during testing.
