<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- SPDX-FileCopyrightText: 2026 Tommy Bazire -->

# Home Assistant Add-ons
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)
[![Add-on: eebus_bridge](https://img.shields.io/badge/add--on-eebus__bridge-41BDF5.svg)](./eebus_bridge)
[![Add-on: eebus_bridge_dev](https://img.shields.io/badge/add--on-eebus__bridge__dev-orange.svg)](./eebus_bridge_dev)
[![ko-fi](https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/X3E023PP7J)

A collection of Home Assistant add-ons maintained by **Tommy Bazire**.

## Available add-ons

| Add-on | Description | Status |
|--------|-------------|--------|
| [**EEBUS Bridge**](./eebus_bridge) | Generic EEBUS bridge. Pairs with any EEBUS device on the local network (heat pumps, wallboxes, inverters, …) and exposes its measurements as Home Assistant sensors via MQTT discovery. | stable |
| [**EEBUS Bridge (Dev)**](./eebus_bridge_dev) | Development channel of EEBUS Bridge. Same code as the production add-on, used to preview upcoming changes. ⚠️ Mutually exclusive with the production add-on (same SHIP port, MQTT ClientID and discovery topics — only one can run at a time). | dev |

## Installation

To use these add-ons in your Home Assistant instance:

1. In Home Assistant, go to **Settings → Add-ons → Add-on Store**.
2. Click the **⋯** menu (top right) → **Repositories**.
3. Add the following URL:
   ```
   https://github.com/tbazire/homeassistant-addons
   ```
4. The add-ons from this repository now appear in the store. Click **EEBUS Bridge** (or **EEBUS Bridge (Dev)** for the development channel) → **Install**.

> 💡 Most users want the **production** add-on. The **Dev** channel exists to
> preview upcoming changes and collect early feedback; it is mutually exclusive
> with the production add-on and should not be run alongside it.

Detailed installation and configuration instructions live in each add-on's own `README.md`.

## Security

This repository is **public** and follows a **Security by Design** approach:

- No credentials, certificates or private keys are stored in the repository.
- Secrets (MQTT password, SHIP pairing secret) are injected by Home Assistant at runtime and never logged.
- Add-on containers run with the minimum set of privileges required (see each add-on's `config.yaml` for the exact permissions and their justification).
- Container images are multi-arch and **signed with Cosign** (keyless, via GitHub OIDC).

Please report security issues responsibly — see [`SECURITY.md`](./SECURITY.md).

## License

Licensed under the **Apache License, Version 2.0**. See [`LICENSE`](./LICENSE) and [`NOTICE`](./NOTICE).

Third-party attributions are listed in [`NOTICE`](./NOTICE).
