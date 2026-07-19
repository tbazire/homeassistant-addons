// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Command eebus-bridge is the MQTT/HA half of the eebus_bridge add-on.
//
// It spawns the eebusd daemon as a subprocess, parses its NDJSON stdout stream,
// and publishes Home Assistant MQTT discovery messages + sensor states. It has
// no knowledge of the EEBUS/SHIP/SPINE protocol — that stays in eebusd.
//
// Configuration comes entirely from EEBUS_* environment variables, which the
// add-on's run.sh (bashio) sets from the HA options.

package main

import (
	"fmt"
	"os"

	"eebus-bridge/internal"
)

func main() {
	cfg, err := internal.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid configuration: %v\n", err)
		os.Exit(2)
	}

	logger := internal.NewLogger(cfg.LogLevel)
	logger.Info("eebus-bridge starting", "config", cfg.Redacted())

	orch := internal.NewOrchestrator(cfg, logger)
	if err := orch.Run(); err != nil {
		logger.Error("eebus-bridge failed", "err", err.Error())
		os.Exit(1)
	}
	logger.Info("eebus-bridge stopped")
}
