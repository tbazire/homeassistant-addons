<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- SPDX-FileCopyrightText: 2026 Tommy Bazire -->

# Contributing

Thanks for your interest in improving this project! This document describes
how to set up a development environment and the conventions to follow.

## Development setup

### Prerequisites

- **Go** 1.24+ (for `eebusd` and `eebus-bridge`).
- **Docker** (with BuildKit / buildx) for building the add-on image locally.
- **Python** 3.10+ (only for the add-on config validation tests).
- Optional: `shellcheck`, `yamllint` (matched by CI).

### Layout

```
homeassistant-addons/
├── eebus_bridge/         # the add-on
│   ├── eebusd/           # the EEBUS daemon (Go, vendored)
│   ├── bridge/           # the MQTT/HA bridge (Go)
│   ├── config.yaml       # HA add-on schema
│   ├── Dockerfile        # multi-stage build
│   └── run.sh            # s6 entrypoint
└── .github/workflows/    # CI
```

### Building locally

```bash
# Build eebusd and eebus-bridge natively (fast feedback loop)
cd eebus_bridge/eebusd && go build ./...
cd eebus_bridge/bridge  && go build ./...

# Run unit tests
go test ./...

# Build the add-on Docker image (uses HA base images)
cd eebus_bridge
docker build --build-arg BUILD_FROM=ghcr.io/home-assistant/amd64-base:3.12-alpine3.21 -t eebus-bridge:dev .
```

## Conventions

### Commits — Conventional Commits

We use [Conventional Commits](https://www.conventionalcommits.org/) to keep the
history clean and to enable automated changelog generation later.

```
<type>(<scope>): <subject>

types:  feat fix docs style refactor perf test chore ci security
scope:  eebusd | bridge | addon | ci | docs | repo
```

Examples:
- `feat(bridge): publish MQTT discovery for measurements`
- `fix(eebusd): throttle DataChange-triggered pulls`
- `security(addon): drop to non-root user in runtime image`
- `docs: document SKI pairing in README`

### Branching

- `main` is the protected release branch. PRs target `main`.
- Feature branches: `feat/<short-description>`, `fix/<issue>`, etc.
- Releases are cut by tagging `main` with `v<version>` (e.g. `v0.1.0`).

### License header

Every source file (`.go`, `.sh`, `.yaml`, `.py`) must start with:

```
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: 2026 Tommy Bazire
```

(Go files use `//` line comments, shell/yaml/python use `#`.) CI checks this.

### Security review checklist

Before opening a PR that touches code, double-check:

- [ ] No secret / password / key in the diff.
- [ ] No new dependency without checking its license (must be Apache-2.0 /
      MIT / BSD / EPL/EDL compatible).
- [ ] Logs do not leak credentials.
- [ ] If a new HA permission is required, it is justified in `config.yaml`
      and documented in `SECURITY.md`.
- [ ] `eebus_bridge/tests/test_config_validation.py` still passes.

### Tests

- Go unit tests (`*_test.go`) are mandatory for new logic.
- The add-on `config.yaml` is validated by
  `eebus_bridge/tests/test_config_validation.py` — keep it green.
- Manual test procedure:
  `eebus_bridge/tests/test_checklist.md`.

## Pull requests

- One logical change per PR.
- Include updated tests.
- Update the relevant `CHANGELOG.md` under an `Unreleased` section.
- Link any issue your PR closes (`Closes #123`).

## Code of Conduct

Participation in this project is governed by the
[Code of Conduct](./CODE_OF_CONDUCT.md). Please be excellent to each other.
