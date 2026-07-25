// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package scanner: unit.go — unit normalization for Home Assistant compatibility.
//
// EEBUS uses "degC" but Home Assistant expects "°C" for temperature device class.

package scanner

import "strings"

// normalizeUnit converts EEBUS unit strings to Home Assistant compatible units.
// This ensures MQTT discovery messages are accepted by HA without errors.
func normalizeUnit(unit string) string {
	if unit == "" {
		return ""
	}

	// degC → °C (temperature)
	if strings.EqualFold(unit, "degC") {
		return "°C"
	}

	// degF → °F (temperature)
	if strings.EqualFold(unit, "degF") {
		return "°F"
	}

	// Add more conversions here if needed

	return unit
}
