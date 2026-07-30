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

// HAClimate is the discovery payload for a climate entity. Used by OHPCF to
// expose heat-pump compressor scheduling (modes off/auto + presets pause/resume).
// Mode/preset command topics receive the user's intent; state/action topics
// carry the device's current state. All topics are absolute.
type HAClimate struct {
	Name                   string    `json:"name"`
	UniqueID               string    `json:"unique_id"`
	Modes                  []string  `json:"modes"`
	ModeCommandTopic       string    `json:"mode_command_topic"`
	ModeStateTopic         string    `json:"mode_state_topic"`
	ActionTopic            string    `json:"action_topic,omitempty"`
	PresetModes            []string  `json:"preset_modes,omitempty"`
	PresetModeCommandTopic string    `json:"preset_mode_command_topic,omitempty"`
	PresetModeStateTopic   string    `json:"preset_mode_state_topic,omitempty"`
	Device                 *HADevice `json:"device,omitempty"`
}

// HANumber is the discovery payload for a number entity (setpoint/slider).
// Reserved for future write use cases (LPC power limit, …).
type HANumber struct {
	Name              string    `json:"name"`
	UniqueID          string    `json:"unique_id"`
	CommandTopic      string    `json:"command_topic"`
	StateTopic        string    `json:"state_topic"`
	UnitOfMeasurement string    `json:"unit_of_measurement,omitempty"`
	Min               *float64  `json:"min,omitempty"`
	Max               *float64  `json:"max,omitempty"`
	Step              *float64  `json:"step,omitempty"`
	Device            *HADevice `json:"device,omitempty"`
}

// HASwitch is the discovery payload for a switch entity (binary on/off).
// Reserved for future write use cases.
type HASwitch struct {
	Name         string    `json:"name"`
	UniqueID     string    `json:"unique_id"`
	CommandTopic string    `json:"command_topic"`
	StateTopic   string    `json:"state_topic"`
	PayloadOn    string    `json:"payload_on,omitempty"`
	PayloadOff   string    `json:"payload_off,omitempty"`
	Device       *HADevice `json:"device,omitempty"`
}

// HASelect is the discovery payload for a select entity (enum).
// Reserved for future write use cases.
type HASelect struct {
	Name         string    `json:"name"`
	UniqueID     string    `json:"unique_id"`
	CommandTopic string    `json:"command_topic"`
	StateTopic   string    `json:"state_topic"`
	Options      []string  `json:"options"`
	Device       *HADevice `json:"device,omitempty"`
}

// Discovery encodes the full set of topics + payloads needed to publish one
// event: the discovery config (topic + payload) and the state (topic + value).
//
// Config is an opaque payload (one of *HASensor / *HAClimate / *HANumber /
// *HASwitch / *HASelect) so the orchestrator can publish it without caring
// about the concrete HA component type. CommandTopics lists the MQTT topics
// the bridge must subscribe to in order to receive commands for this entity
// (empty for sensors). The orchestrator re-applies subscriptions on MQTT
// reconnect.
type Discovery struct {
	ConfigTopic   string
	Config        any
	StateTopic    string
	StateValue    string
	ActionTopic   string   // optional, for climate action state
	ActionValue   string   // optional initial action value
	CommandTopics []string // topics to subscribe to (write entities only)
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
	// me.Value is *float64: nil events are filtered upstream (parseLine), so
	// here it is always non-nil. Defensive guard kept for direct callers/tests.
	stateValue := ""
	if me.Value != nil {
		stateValue = formatValue(*me.Value, me.Scale)
	}

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

// OnControllable maps a "controllable" event into a Discovery descriptor for
// the matching HA control entity. The entity type is chosen by the use case
// itself (carried in c.Component), so the bridge stays generic: when a new use
// case ships (LPC → number, OPEV → number, …) this method picks it up without
// changes as long as the component is one we model.
//
// For climate (OHPCF) it builds modes [off, auto] + presets [pause, resume] if
// the use case exposes those actions. The initial mode/action/state are seeded
// from c.State. CommandTopics lists every topic the bridge must subscribe to so
// the orchestrator can route inbound HA commands back to eebusd.
func (m *Mapper) OnControllable(c *Controllable) Discovery {
	uid := uniqueID(c.SKI, c.Entity, c.UseCase)
	disc := Discovery{}

	// Already announced: refresh the state/action only (no discovery re-publish).
	if m.announced[uid] {
		switch c.Component {
		case "climate":
			disc.StateTopic = fmt.Sprintf("%s/%s/%s/%s/mode/state", m.prefix, c.SKI, entitySafe(c.Entity), c.UseCase)
			disc.StateValue = climateModeFromHAAction(c.State)
			disc.ActionTopic = fmt.Sprintf("%s/%s/%s/%s/action/state", m.prefix, c.SKI, entitySafe(c.Entity), c.UseCase)
			disc.ActionValue = c.State
		case "number":
			// The number entity's state topic carries the raw value (e.g. the
			// watts limit). On refresh, c.State is that value as formatted by
			// eebusd (or "" when no limit is active).
			disc.StateTopic = fmt.Sprintf("%s/%s/%s/%s/value/state", m.prefix, c.SKI, entitySafe(c.Entity), c.UseCase)
			disc.StateValue = c.State
		}
		return disc
	}

	dev := m.devices[c.SKI]
	if dev == nil {
		// Controllable arrived before any manufacturer line: synthesise a
		// minimal device block so the control entity attaches to something.
		dev = &HADevice{
			Identifiers: []string{c.SKI},
			Name:        defaultDeviceName(c.SKI),
		}
		m.devices[c.SKI] = dev
	}

	// State is reused as both mode_state and action initial value (HA climate
	// distinguishes them, but for OHPCF the same string suffices initially).
	initial := c.State

	switch c.Component {
	case "climate":
		disc.ConfigTopic = fmt.Sprintf("%s/climate/eebus_bridge/%s/config", m.discovery, uid)
		modeState := fmt.Sprintf("%s/%s/%s/%s/mode/state", m.prefix, c.SKI, entitySafe(c.Entity), c.UseCase)
		modeCmd := fmt.Sprintf("%s/%s/%s/%s/mode/cmd", m.prefix, c.SKI, entitySafe(c.Entity), c.UseCase)
		actionTopic := fmt.Sprintf("%s/%s/%s/%s/action/state", m.prefix, c.SKI, entitySafe(c.Entity), c.UseCase)
		presetState := fmt.Sprintf("%s/%s/%s/%s/preset/state", m.prefix, c.SKI, entitySafe(c.Entity), c.UseCase)
		presetCmd := fmt.Sprintf("%s/%s/%s/%s/preset/cmd", m.prefix, c.SKI, entitySafe(c.Entity), c.UseCase)

		presets := []string{}
		hasPause := contains(c.Actions, "pause")
		hasResume := contains(c.Actions, "resume")
		if hasPause {
			presets = append(presets, "pause")
		}
		if hasResume {
			presets = append(presets, "resume")
		}

		climate := &HAClimate{
			Name:                   climateName(c),
			UniqueID:               uid,
			Modes:                  []string{"off", "auto"},
			ModeCommandTopic:       modeCmd,
			ModeStateTopic:         modeState,
			ActionTopic:            actionTopic,
			PresetModes:            presets,
			PresetModeCommandTopic: presetCmd,
			PresetModeStateTopic:   presetState,
			Device:                 dev,
		}
		disc.Config = climate
		disc.StateTopic = modeState
		disc.StateValue = climateModeFromHAAction(initial)
		disc.ActionTopic = actionTopic
		disc.ActionValue = initial
		disc.CommandTopics = []string{modeCmd, presetCmd}
	case "number":
		// A numeric setpoint (e.g. LPC power limit). The unit is declared by
		// the use case (carried in c.Unit). A single value topic carries the
		// current limit and receives user input; the orchestrator's
		// decodeHACommand routes "/value/cmd" payloads to <uc>.set.
		disc.ConfigTopic = fmt.Sprintf("%s/number/eebus_bridge/%s/config", m.discovery, uid)
		valueState := fmt.Sprintf("%s/%s/%s/%s/value/state", m.prefix, c.SKI, entitySafe(c.Entity), c.UseCase)
		valueCmd := fmt.Sprintf("%s/%s/%s/%s/value/cmd", m.prefix, c.SKI, entitySafe(c.Entity), c.UseCase)
		number := &HANumber{
			Name:              controlName(c),
			UniqueID:          uid,
			CommandTopic:      valueCmd,
			StateTopic:        valueState,
			UnitOfMeasurement: c.Unit,
			Device:            dev,
		}
		disc.Config = number
		disc.StateTopic = valueState
		disc.StateValue = c.State
		disc.CommandTopics = []string{valueCmd}
	default:
		// Unknown component: log via the empty discovery so the orchestrator
		// skips publishing. New components (switch/select) will be wired here
		// as their use cases ship.
		return disc
	}

	m.announced[uid] = true
	return disc
}

// controlName builds a human-readable name for a generic control entity. It
// prefers the entity type (e.g. "HeatPumpAppliance"), then the unit, then
// falls back to the use case name.
func controlName(c *Controllable) string {
	if c.EntityType != "" {
		if c.Unit != "" {
			return fmt.Sprintf("EEBUS %s limit (%s)", c.EntityType, c.Unit)
		}
		return fmt.Sprintf("EEBUS %s control", c.EntityType)
	}
	if c.Unit != "" {
		return fmt.Sprintf("EEBUS limit (%s)", c.Unit)
	}
	return fmt.Sprintf("EEBUS control (%s)", c.UseCase)
}

// climateName builds a human-readable name for a climate control entity.
func climateName(c *Controllable) string {
	if c.EntityType != "" {
		return fmt.Sprintf("EEBUS %s control", c.EntityType)
	}
	return fmt.Sprintf("EEBUS control (%s)", c.UseCase)
}

// climateModeFromHAAction maps the initial HA action string back to the
// climate mode for the mode_state topic. action "off" → mode "off"; anything
// else → "auto" (the device is running, so we reflect the active mode).
func climateModeFromHAAction(action string) string {
	switch action {
	case "off", "":
		return "off"
	default:
		return "auto"
	}
}

func contains(slice []string, s string) bool {
	for _, x := range slice {
		if x == s {
			return true
		}
	}
	return false
}

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
