<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- SPDX-FileCopyrightText: 2026 Tommy Bazire -->

# Security Policy

This repository is **public** and is designed around a **Security by Design**
and **Public by Design** approach. This document describes the threat model,
the rules that protect users, and how to report a vulnerability.

## Supported versions

Only the latest released version of each add-on receives security fixes.
Versions are tagged `v<version>` on the `main` branch.

| Add-on | Supported |
|--------|-----------|
| `eebus_bridge` | latest `v0.x` release |

## Reporting a vulnerability

**Please do NOT open a public GitHub issue for security problems.**

Instead, report vulnerabilities privately using one of:

- **GitHub Security Advisories** (preferred): go to the
  [Security tab](https://github.com/tbazire/homeassistant-addons/security/advisories/new)
  of the repository and click **Report a vulnerability**.
- Email the maintainer (see the GitHub profile).

Please include:
- a description of the issue and its potential impact;
- the affected version (or commit SHA);
- reproduction steps, a proof-of-concept, or an attack scenario;
- suggested mitigation or fix if you have one.

You should receive an acknowledgement within **72 hours**. Security fixes are
released as patch versions and disclosed in the relevant `CHANGELOG.md` once a
fix is available.

## Threat model

| Asset | Threat | Mitigation |
|-------|--------|------------|
| SHIP private key | Theft → impersonation of the add-on to EEBUS devices | Generated in-container at first start, stored under `/data` (HA persistent, private), mode `0600`. Never logged, never shipped in the image. |
| MQTT credentials | Leakage → data exfiltration / injection | Resolved from the HA Supervisor (Mosquitto add-on) by default; user-provided credentials are stored encrypted by HA. Never logged (the bridge redacts known secret values). |
| SHIP pairing secret | Hardcoded in image | Never hardcoded; injected as an HA `password` option. |
| Network exposure | Unauthorized inbound connections | Only the SHIP websocket port and mDNS multicast are required; `host_network: true` is the documented, justified exception. No `privileged`, `full_access`, `host_pid`, `host_dbus`, `docker_api`. |
| Image supply chain | Tampered image | Multi-arch images are **signed with Cosign** (keyless, GitHub OIDC). Users can verify with `cosign verify`. |
| Logs | Secret leakage via logs | Both `eebusd` and `eebus-bridge` redact known secret values. MQTT credentials are resolved in `run.sh` and only ever passed as env vars to the bridge. |
| Container escape | Privilege escalation | Runtime container runs as a **non-root** user. AppArmor is left to HA's internal profile. |

## Security by Design — mandatory rules

The following rules apply to every file in this repository and are enforced by
review (and partially by CI):

1. **No password in source code.**
2. **No private key in the repository.**
3. **No user certificate in Git.**
4. **No hardcoded secret.**
5. **No access token in source code.**
6. **No sensitive data in examples** (use placeholders like `<your-broker>`).
7. **All sensitive parameters are injected by Home Assistant** (options or Supervisor service discovery).
8. **Logs never expose sensitive information.**
9. **Persistent paths are strictly scoped** to HA's `/data`.
10. **Unused dependencies are removed.**
11. **System permissions are minimal.**

## Container hardening

The add-on container follows HA's hardening recommendations:

- **Minimum privileges**: only `host_network: true` is enabled, and it is
  technically required (EEBUS uses mDNS multicast on UDP 5353 and expects an
  inbound TCP connection from the LAN device).
- **No `privileged` mode**, no `full_access`, no `host_pid`, no `host_dbus`,
  no `host_ipc`, no `docker_api`. Absence is asserted by
  `eebus_bridge/tests/test_config_validation.py`.
- **No `map`** of host folders (only HA's own `/data` is used, via the add-on
  runtime).
- **Non-root execution**: the runtime image drops to a dedicated non-root user.
- **Reduced attack surface**: static Go binaries (`CGO_ENABLED=0`), no shell
  in the runtime path except `run.sh`, minimal base image.

## EEBUS certificate management

EEBUS relies on a self-signed certificate (EC P-256) to establish the SHIP
mutual TLS channel. Each install owns its own key pair:

- **Generation**: `eebusd` generates the key pair on first start (via
  `ship-go/cert.CreateCertificate`) and persists it under `/data/eebus`.
- **Storage**: the persistent directory is HA's `/data`, which is private to
  the add-on and survives restarts/backups.
- **Import**: advanced users can supply their own cert/key via the add-on
  options (future) — they will be stored the same way and never logged.
- **Loss**: losing the key forces re-pairing with every EEBUS device. The
  `README.md` documents how to re-pair.
- **Leak prevention**: `.gitignore` excludes `certs/`, `*.key`, `*.crt`,
  `*.pem`, `ringbuffer.json`. CI scans for accidental commits of private keys.

## License

This project is licensed under the Apache License 2.0 — see [`LICENSE`](./LICENSE).
