#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: 2026 Tommy Bazire
#
# Static validation of the eebus_bridge add-on config.yaml.
#
# This is a security gate: it asserts that the add-on never asks for more
# permissions than necessary. Run with `python test_config_validation.py`
# (no test framework required — only the stdlib + PyYAML).
#
# Exit code 0 = all checks pass; 1 = at least one check failed.

import sys
from pathlib import Path

try:
    import yaml  # type: ignore
except ImportError:
    print("ERROR: PyYAML is required (pip install pyyaml)", file=sys.stderr)
    sys.exit(2)


REPO_ROOT = Path(__file__).resolve().parents[2]
ADDON_DIR = Path(__file__).resolve().parents[1]
CONFIG_PATH = ADDON_DIR / "config.yaml"
LICENSE_PATH = REPO_ROOT / "LICENSE"

# Permissions that would grant the add-on excessive host access. Each one is
# explicitly justified (or absent) in the security posture.
FORBIDDEN_KEYS = (
    "privileged",
    "full_access",
    "host_pid",
    "host_dbus",
    "host_ipc",
    "docker_api",
    "usb",
    "gpio",
    "uart",
    "device",
)

REQUIRED_ARCHES = ("aarch64", "amd64", "armhf", "armv7", "i386")

errors: list[str] = []


def check(cond: bool, message: str) -> None:
    status = "OK " if cond else "FAIL"
    print(f"  [{status}] {message}")
    if not cond:
        errors.append(message)


def main() -> int:
    print(f"Validating {CONFIG_PATH.relative_to(REPO_ROOT)}")

    if not CONFIG_PATH.is_file():
        print(f"FAIL: {CONFIG_PATH} does not exist", file=sys.stderr)
        return 1

    with CONFIG_PATH.open("r", encoding="utf-8") as f:
        cfg = yaml.safe_load(f)

    if not isinstance(cfg, dict):
        print("FAIL: config.yaml is not a mapping", file=sys.stderr)
        return 1

    # --- Required top-level fields --------------------------------------------------
    print("Top-level fields:")
    for key in ("name", "version", "slug", "description", "arch", "init",
                "startup", "boot", "host_network", "services", "options", "schema"):
        check(key in cfg, f"has '{key}'")

    # --- Slug / version consistency -------------------------------------------------
    print("Identity:")
    check(isinstance(cfg.get("slug"), str) and cfg["slug"] == "eebus_bridge",
          f"slug is 'eebus_bridge' (got {cfg.get('slug')!r})")
    check(isinstance(cfg.get("version"), str) and cfg["version"],
          f"version is a non-empty string (got {cfg.get('version')!r})")

    # --- Architectures --------------------------------------------------------------
    print("Architectures:")
    arches = cfg.get("arch", [])
    check(isinstance(arches, list) and len(arches) == len(REQUIRED_ARCHES),
          f"declares all {len(REQUIRED_ARCHES)} arches")
    for a in REQUIRED_ARCHES:
        check(a in arches, f"supports {a}")

    # --- Security posture -----------------------------------------------------------
    print("Security posture (forbidden permissions):")
    for key in FORBIDDEN_KEYS:
        check(key not in cfg, f"does NOT request '{key}'")

    # init MUST be false (s6-overlay v3 base images).
    print("Lifecycle:")
    check(cfg.get("init") is False, "init is false (required by s6-overlay v3)")
    # host_network MUST be true (mDNS multicast + inbound SHIP TCP).
    check(cfg.get("host_network") is True,
          "host_network is true (required for EEBUS mDNS + inbound SHIP)")

    # --- Services ------------------------------------------------------------------
    print("Services:")
    services = cfg.get("services") or []
    check(isinstance(services, list) and any(
        isinstance(s, str) and s.startswith("mqtt") for s in services),
        "requires mqtt service discovery")

    # --- options / schema alignment ------------------------------------------------
    print("Options/schema alignment:")
    options = cfg.get("options") or {}
    schema = cfg.get("schema") or {}
    check(isinstance(options, dict) and isinstance(schema, dict),
          "options and schema are mappings")
    if isinstance(options, dict) and isinstance(schema, dict):
        # Every key in options must have a matching key in schema.
        for key in options:
            check(key in schema, f"option '{key}' has a schema entry")
        # Secret-bearing fields must use a password type in schema.
        for sensitive in ("pairing.secret",):
            section, leaf = sensitive.split(".", 1)
            sec_section = schema.get(section) or {}
            if isinstance(sec_section, dict) and leaf in sec_section:
                leaf_type = sec_section[leaf]
                check(isinstance(leaf_type, str) and "password" in leaf_type,
                      f"'{sensitive}' schema is a password type (got {leaf_type!r})")

    # --- License file ---------------------------------------------------------------
    print("License:")
    check(LICENSE_PATH.is_file(), f"LICENSE exists at repo root")
    if LICENSE_PATH.is_file():
        text = LICENSE_PATH.read_text(encoding="utf-8")
        check("Apache License" in text and "Version 2.0" in text,
              "LICENSE contains Apache 2.0 header")

    # --- Conclusion -----------------------------------------------------------------
    print()
    if errors:
        print(f"FAIL: {len(errors)} check(s) failed:")
        for e in errors:
            print(f"  - {e}")
        return 1
    print("PASS: all checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
