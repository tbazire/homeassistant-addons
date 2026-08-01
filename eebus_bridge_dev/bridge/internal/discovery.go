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

// HABinarySensor is the discovery payload for a binary_sensor entity (on/off).
// Used by use-case read signals that are booleans (e.g. OHPCF is_pausable). The
// payload_on/payload_off strings match the value format the use case emits.
type HABinarySensor struct {
	Name        string    `json:"name"`
	UniqueID    string    `json:"unique_id"`
	StateTopic  string    `json:"state_topic"`
	DeviceClass string    `json:"device_class,omitempty"`
	PayloadOn   string    `json:"payload_on,omitempty"`
	PayloadOff  string    `json:"payload_off,omitempty"`
	Device      *HADevice `json:"device,omitempty"`
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

// OnUcSignal maps a use-case read-signal event into a Discovery descriptor for
// a typed HA sensor. The HA component + device class + unit are chosen from the
// signal's value type, so the same method handles every use case's signals
// (OHPCF today, LPC/others later) without per-use-case logic.
//
// Component selection:
//   - boolean values            → binary_sensor
//   - number/date_time/duration → sensor with the matching device_class
//
// The sensor attaches to the same HA device as the control entity (same SKI)
// and is announced once per (ski, entity, usecase, signal); subsequent lines
// only refresh the state.
func (m *Mapper) OnUcSignal(s *UcSignal) Discovery {
	uid := ucSignalUniqueID(s.SKI, s.Entity, s.UseCase, s.Signal)
	stateTopic := fmt.Sprintf("%s/%s/%s/%s/%s/state", m.prefix, s.SKI, entitySafe(s.Entity), s.UseCase, s.Signal)
	disc := Discovery{StateTopic: stateTopic, StateValue: signalStateValue(s)}

	// Already announced: state-only refresh.
	if m.announced[uid] {
		return disc
	}

	dev := m.devices[s.SKI]
	if dev == nil {
		// Signal arrived before any manufacturer line: synthesise a minimal
		// device block so the sensor attaches to something.
		dev = &HADevice{
			Identifiers: []string{s.SKI},
			Name:        defaultDeviceName(s.SKI),
		}
		m.devices[s.SKI] = dev
	}

	switch s.ValueType {
	case "boolean":
		disc.ConfigTopic = fmt.Sprintf("%s/binary_sensor/eebus_bridge/%s/config", m.discovery, uid)
		disc.Config = &HABinarySensor{
			Name:        signalName(s.UseCase, s.Signal),
			UniqueID:    uid,
			StateTopic:  stateTopic,
			DeviceClass: signalBinaryDeviceClass(s.Signal),
			PayloadOn:   "true",
			PayloadOff:  "false",
			Device:      dev,
		}
	default:
		// number / date_time / duration → typed sensor.
		disc.ConfigTopic = fmt.Sprintf("%s/sensor/eebus_bridge/%s/config", m.discovery, uid)
		unit, class, stateClass := signalSensorAttrs(s.Signal, s.ValueType, s.Unit)
		disc.Config = &HASensor{
			Name:              signalName(s.UseCase, s.Signal),
			StateTopic:        stateTopic,
			UniqueID:          uid,
			UnitOfMeasurement: unit,
			DeviceClass:       class,
			StateClass:        stateClass,
			Device:            dev,
		}
		// The state value for duration signals is published in the unit the
		// sensor declares (minutes), converted from the wire's seconds.
		if s.ValueType == "duration" {
			disc.StateValue = signalDurationMinutes(s.Value)
		}
	}

	m.announced[uid] = true
	return disc
}

// signalStateValue renders the value to publish on the state topic. Booleans
// pass through as-is ("true"/"false"); durations are converted to minutes (the
// HA duration unit); numbers and date_times pass through verbatim.
func signalStateValue(s *UcSignal) string {
	if s.ValueType == "duration" {
		return signalDurationMinutes(s.Value)
	}
	return s.Value
}

// signalDurationMinutes converts a wire duration value (seconds, integer
// string) into minutes as a rounded integer string, which is the unit the HA
// duration sensor uses. Non-numeric input is returned unchanged so a bad value
// never blanks the sensor.
func signalDurationMinutes(secondsStr string) string {
	v, err := parseSeconds(secondsStr)
	if err != nil {
		return secondsStr
	}
	minutes := v / 60
	return fmt.Sprintf("%d", minutes)
}

// signalName builds a human-readable sensor name from the use case + signal.
func signalName(useCase, signal string) string {
	return fmt.Sprintf("EEBUS %s %s", useCase, prettifySignal(signal))
}

// prettifySignal turns "requested_power" into "requested power" for display.
func prettifySignal(s string) string {
	return strings.ReplaceAll(s, "_", " ")
}

// signalSensorAttrs returns the (unit, device_class, state_class) triple for a
// non-boolean signal. Derived from the value type + wire unit (NOT from the
// signal name) so the same logic serves any use case — OHPCF, LPC, and future
// ones — without a growing name list. Falls back to a plain sensor (no class)
// when the type/unit do not map to a known HA class.
func signalSensorAttrs(signal, valueType, wireUnit string) (unit, class, stateClass string) {
	switch valueType {
	case "date_time":
		// An absolute instant in time.
		return "", "timestamp", ""
	case "duration":
		// Duration expressed in minutes (the HA duration unit).
		return "min", "duration", "measurement"
	case "number":
		// Derive the device class from the unit. W → power, A → current, V →
		// voltage, etc. reuse the measurement-side helper.
		return wireUnit, deviceClassFor("", wireUnit), "measurement"
	}
	// Unknown value type: fall back to the wire unit + no class.
	return wireUnit, "", ""
}

// signalBinaryDeviceClass picks a HA binary_sensor device_class for a boolean
// signal. OHPCF capability flags read as "is the device able to…", which maps
// best to "running"/"heat" loosely; we use "running" for pausable/stoppable
// (a process that can be paused is "running-capable") and "power" for
// availability. These are display hints only — the value itself is what matters.
func signalBinaryDeviceClass(signal string) string {
	switch signal {
	case "is_pausable", "is_stoppable":
		return "running"
	case "is_available":
		return "power"
	}
	return ""
}

// ucSignalUniqueID builds a stable HA unique_id for one use-case signal. The
// use case + signal make it distinct from the control entity's unique_id and
// from other use cases' signals on the same entity.
func ucSignalUniqueID(ski, entity, useCase, signal string) string {
	return uniqueID(ski, entity, useCase+"_"+signal)
}

// parseSeconds parses an integer seconds string. Centralised so the duration
// conversion has one error path.
func parseSeconds(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
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
