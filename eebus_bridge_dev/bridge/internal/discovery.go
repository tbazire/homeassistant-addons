// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package internal: discovery.go — pure EEBUS → Home Assistant mapping.
//
// This file has ZERO dependencies on MQTT or on the EEBUS libraries. It only
// consumes the typed events produced by the NDJSON parser and produces HA
// discovery + state payloads as plain Go structs. This makes the mapping
// trivially unit-testable.
//
// Naming rules:
//   - One HA device per EEBUS gateway (keyed by SKI).
//   - One HA sensor per (ski, entity, measurement_id), with a stable unique_id.
//   - Device names come from the manufacturer line; if absent, fall back to
//     the SKI tail so the device still shows up in HA.
//   - Sensor names are derived from type+scope+unit when available.

package internal

import (
	"fmt"
	"strings"
)

// HADevice is the Home Assistant "device" block embedded in every discovery
// payload. It groups all sensors of one EEBUS gateway.
type HADevice struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer,omitempty"`
	Model        string   `json:"model,omitempty"`
	SWVersion    string   `json:"sw_version,omitempty"`
	HWVersion    string   `json:"hw_version,omitempty"`
}

// HASensor is the discovery payload for one sensor entity. State and command
// topics are absolute (MQTT expects the full topic path).
type HASensor struct {
	Name              string    `json:"name"`
	StateTopic        string    `json:"state_topic"`
	UniqueID          string    `json:"unique_id"`
	UnitOfMeasurement string    `json:"unit_of_measurement,omitempty"`
	DeviceClass       string    `json:"device_class,omitempty"`
	StateClass        string    `json:"state_class,omitempty"`
	Device            *HADevice `json:"device,omitempty"`
}

// Discovery encodes the full set of topics + payloads needed to publish one
// event: the discovery config (topic + payload) and the state (topic + value).
type Discovery struct {
	ConfigTopic string
	Config      *HASensor
	StateTopic  string
	StateValue  string
}

// Mapper turns EEBUS events into HA Discovery payloads. It is stateful: it
// remembers the device info per SKI (so subsequent sensors attach to the right
// HA device) and the set of already-announced unique_ids (so we do not spam
// discovery messages on every measurement refresh).
type Mapper struct {
	prefix    string // MQTT state prefix, e.g. "eebus"
	discovery string // HA discovery prefix, e.g. "homeassistant"

	devices   map[string]*HADevice // ski -> device block
	announced map[string]bool      // unique_id already published
}

// NewMapper returns a Mapper using the given MQTT prefixes.
func NewMapper(statePrefix, discoveryPrefix string) *Mapper {
	return &Mapper{
		prefix:    strings.Trim(statePrefix, "/"),
		discovery: strings.Trim(discoveryPrefix, "/"),
		devices:   make(map[string]*HADevice),
		announced: make(map[string]bool),
	}
}

// OnManufacturer updates (or creates) the HA device block for a SKI. Called
// whenever a "manufacturer" kind line arrives. Returns the resulting device
// block (the caller may use it to publish an updated device registry).
func (m *Mapper) OnManufacturer(mf *Manufacturer) *HADevice {
	d := m.devices[mf.SKI]
	if d == nil {
		d = &HADevice{Identifiers: []string{mf.SKI}}
		m.devices[mf.SKI] = d
	}
	if mf.DeviceName != "" {
		d.Name = mf.DeviceName
	} else if d.Name == "" {
		d.Name = defaultDeviceName(mf.SKI)
	}
	if mf.BrandName != "" {
		d.Manufacturer = mf.BrandName
	}
	if mf.DeviceCode != "" {
		d.Model = mf.DeviceCode
	} else if mf.DeviceName != "" {
		d.Model = mf.DeviceName
	}
	if mf.SoftwareRevision != "" {
		d.SWVersion = mf.SoftwareRevision
	}
	if mf.HardwareRevision != "" {
		d.HWVersion = mf.HardwareRevision
	}
	return d
}

// OnMeasurement maps a measurement event into a Discovery descriptor. If the
// sensor has already been announced, only the StateTopic/StateValue are filled
// (Config is nil) so the caller skips the discovery publish.
func (m *Mapper) OnMeasurement(me *Measurement) Discovery {
	uid := uniqueID(me.SKI, me.Entity, me.ID)
	stateTopic := fmt.Sprintf("%s/%s/%s/%s/state", m.prefix, me.SKI, entitySafe(me.Entity), me.ID)
	stateValue := formatValue(me.Value, me.Scale)

	disc := Discovery{
		StateTopic: stateTopic,
		StateValue: stateValue,
	}

	if m.announced[uid] {
		return disc // already discovered — state-only update
	}

	dev := m.devices[me.SKI]
	if dev == nil {
		// Measurement arrived before any manufacturer line: synthesise a
		// minimal device block so the sensor still attaches to something.
		dev = &HADevice{
			Identifiers: []string{me.SKI},
			Name:        defaultDeviceName(me.SKI),
		}
		m.devices[me.SKI] = dev
	}

	disc.ConfigTopic = fmt.Sprintf("%s/sensor/eebus_bridge/%s/config", m.discovery, uid)
	disc.Config = &HASensor{
		Name:              sensorName(me),
		StateTopic:        stateTopic,
		UniqueID:          uid,
		UnitOfMeasurement: unitOfMeasurement(me.Unit),
		DeviceClass:       deviceClassFor(me.Type, me.Unit),
		StateClass:        stateClassFor(me.Type),
		Device:            dev,
	}
	m.announced[uid] = true
	return disc
}

// ---- helpers ---------------------------------------------------------------

// uniqueID builds a stable HA unique_id for one measurement. SKI is long
// (40 hex chars); we keep its tail (12 chars = enough disambiguation on a home
// network) to keep the id readable in HA.
func uniqueID(ski, entity, id string) string {
	tail := ski
	if len(tail) > 12 {
		tail = tail[len(tail)-12:]
	}
	return fmt.Sprintf("eebus_%s_%s_%s", tail, entitySafe(entity), id)
}

// entitySafe turns "3.1" into "3_1" so it is a valid MQTT topic segment and a
// valid HA unique_id component.
func entitySafe(e string) string {
	return strings.ReplaceAll(e, ".", "_")
}

func defaultDeviceName(ski string) string {
	tail := ski
	if len(tail) > 8 {
		tail = tail[len(tail)-8:]
	}
	return "EEBUS " + tail
}

func sensorName(me *Measurement) string {
	var parts []string
	if me.Type != "" {
		parts = append(parts, me.Type)
	}
	if me.Scope != "" {
		parts = append(parts, "("+me.Scope+")")
	}
	if me.Entity != "" && me.Entity != "0" {
		parts = append(parts, "entity "+me.Entity)
	}
	if len(parts) == 0 {
		return "measurement " + me.ID
	}
	return strings.Join(parts, " ")
}

// formatValue renders a measurement value compactly: integers without a
// decimal point, floats with up to 3 significant decimals (trailing zeros
// trimmed). eebusd emits value as the scaled number already, so we do not
// re-apply Scale here.
func formatValue(value float64, _ int) string {
	// Integer values render without decimals.
	if value == float64(int64(value)) {
		return fmt.Sprintf("%d", int64(value))
	}
	// Otherwise, render with 3 decimals then trim trailing zeros + dot.
	s := fmt.Sprintf("%.3f", value)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

// unitOfMeasurement normalizes the SPINE unit string to a HA-friendly symbol.
// Unknown units are passed through verbatim (HA will still display them).
func unitOfMeasurement(unit string) string {
	switch strings.ToUpper(unit) {
	case "W":
		return "W"
	case "WH":
		return "Wh"
	case "KWH":
		return "kWh"
	case "A":
		return "A"
	case "V":
		return "V"
	case "HZ":
		return "Hz"
	case "C":
		return "°C"
	case "PERCENT":
		return "%"
	default:
		return unit
	}
}

// deviceClassFor maps a (type, unit) pair to a HA device class. Returns "" if
// no good match — HA will fall back to a plain sensor.
// https://www.home-assistant.io/integrations/sensor/#device_class
func deviceClassFor(typ, unit string) string {
	u := strings.ToUpper(unit)
	t := strings.ToUpper(typ)
	switch {
	case u == "W":
		return "power"
	case u == "WH" || u == "KWH":
		return "energy"
	case u == "A":
		return "current"
	case u == "V":
		return "voltage"
	case u == "HZ":
		return "frequency"
	case u == "C":
		return "temperature"
	case t == "TEMPERATURE":
		return "temperature"
	case t == "ENERGY" || t == "ACTIVE_ENERGY":
		return "energy"
	case t == "POWER" || t == "ACTIVE_POWER":
		return "power"
	}
	return ""
}

// stateClassFor maps a measurement type to a HA state class. Most EEBUS
// measurements are "measurement" (instantaneous); energy counters are "total".
func stateClassFor(typ string) string {
	t := strings.ToUpper(typ)
	switch {
	case t == "ENERGY" || t == "ACTIVE_ENERGY" || strings.Contains(t, "CUMULATED"):
		return "total_increasing"
	case t == "POWER" || t == "ACTIVE_POWER" || t == "VOLTAGE" || t == "CURRENT":
		return "measurement"
	}
	return "measurement"
}
