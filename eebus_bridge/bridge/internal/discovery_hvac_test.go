// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// discovery_hvac_test.go — HVAC mapping tests: select/switch components for
// HVACRoom operation modes, the composed water_heater for DHWCircuit, the
// /mode/cmd command routing, and the °C signal device class.

package internal

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---- HVACRoom: crhsf select / crht number ----------------------------------

func TestOnControllable_Select_HvacRoom(t *testing.T) {
	// crhsf on an HVACRoom renders a select whose options are the supported
	// heating operation modes; the command topic carries the chosen option.
	m := NewMapper("eebus", "homeassistant")
	c := &Controllable{
		Line:       Line{SKI: "ski1", Entity: "2.1"},
		EntityType: "HVACRoom",
		UseCase:    "crhsf",
		Component:  "select",
		Actions:    []string{"auto", "eco", "off", "on"},
		State:      "auto",
	}
	discs := m.OnControllable(c)
	if len(discs) != 1 {
		t.Fatalf("expected 1 discovery, got %d", len(discs))
	}
	d := discs[0]
	if !strings.Contains(d.ConfigTopic, "/select/eebus_bridge/") {
		t.Errorf("config topic = %q, want a select domain topic", d.ConfigTopic)
	}
	sel, ok := d.Config.(*HASelect)
	if !ok {
		t.Fatalf("config is %T, want *HASelect", d.Config)
	}
	if len(sel.Options) != 4 || sel.Options[0] != "auto" {
		t.Errorf("options = %v, want the 4 sorted modes", sel.Options)
	}
	if sel.CommandTopic != "eebus/ski1/2_1/crhsf/mode/cmd" {
		t.Errorf("command topic = %q", sel.CommandTopic)
	}
	if sel.StateTopic != "eebus/ski1/2_1/crhsf/mode/state" {
		t.Errorf("state topic = %q", sel.StateTopic)
	}
	if len(d.CommandTopics) != 1 || d.CommandTopics[0] != sel.CommandTopic {
		t.Errorf("command topics = %v", d.CommandTopics)
	}
	if d.StateTopic != sel.StateTopic || d.StateValue != "auto" {
		t.Errorf("state = (%q, %q), want (mode state, auto)", d.StateTopic, d.StateValue)
	}

	// A subsequent line for the same use case is a state-only refresh.
	refresh := &Controllable{
		Line:       Line{SKI: "ski1", Entity: "2.1"},
		EntityType: "HVACRoom",
		UseCase:    "crhsf",
		Component:  "select",
		Actions:    []string{"auto", "eco", "off", "on"},
		State:      "eco",
	}
	discs = m.OnControllable(refresh)
	if len(discs) != 1 || discs[0].Config != nil {
		t.Fatalf("refresh must be state-only, got %+v", discs)
	}
	if discs[0].StateValue != "eco" || discs[0].StateTopic != sel.StateTopic {
		t.Errorf("refresh state = (%q, %q)", discs[0].StateTopic, discs[0].StateValue)
	}
}

func TestOnControllable_SelectOnOffOnlyRendersSwitch(t *testing.T) {
	// A mode set of exactly on/off renders as a switch (familiar toggle)
	// instead of a two-option select.
	m := NewMapper("eebus", "homeassistant")
	c := &Controllable{
		Line:       Line{SKI: "ski1", Entity: "2.1"},
		EntityType: "HVACRoom",
		UseCase:    "crhsf",
		Component:  "select",
		Actions:    []string{"off", "on"},
		State:      "off",
	}
	discs := m.OnControllable(c)
	if len(discs) != 1 {
		t.Fatalf("expected 1 discovery, got %d", len(discs))
	}
	if !strings.Contains(discs[0].ConfigTopic, "/switch/eebus_bridge/") {
		t.Errorf("config topic = %q, want a switch domain topic", discs[0].ConfigTopic)
	}
	sw, ok := discs[0].Config.(*HASwitch)
	if !ok {
		t.Fatalf("config is %T, want *HASwitch", discs[0].Config)
	}
	if sw.PayloadOn != "on" || sw.PayloadOff != "off" {
		t.Errorf("payloads = (%q, %q), want (on, off)", sw.PayloadOn, sw.PayloadOff)
	}
	if sw.CommandTopic != "eebus/ski1/2_1/crhsf/mode/cmd" {
		t.Errorf("command topic = %q", sw.CommandTopic)
	}
}

func TestOnControllable_Number_Crht(t *testing.T) {
	// crht on an HVACRoom renders a plain number (the room setpoint) with the
	// device-advertised range — it does NOT take part in the water heater
	// composition (that is DHWCircuit-only).
	m := NewMapper("eebus", "homeassistant")
	c := &Controllable{
		Line:       Line{SKI: "ski1", Entity: "2.1"},
		EntityType: "HVACRoom",
		UseCase:    "crht",
		Component:  "number",
		Unit:       "°C",
		Range:      &NumberRange{Min: 5, Max: f64(30), Step: 0.5},
		State:      "21",
	}
	discs := m.OnControllable(c)
	if len(discs) != 1 {
		t.Fatalf("expected 1 discovery, got %d", len(discs))
	}
	num, ok := discs[0].Config.(*HANumber)
	if !ok {
		t.Fatalf("config is %T, want *HANumber", discs[0].Config)
	}
	if num.UnitOfMeasurement != "°C" || num.Min == nil || *num.Max != 30 {
		t.Errorf("number payload wrong: %+v", num)
	}
	if !strings.Contains(discs[0].ConfigTopic, "/number/eebus_bridge/") {
		t.Errorf("config topic = %q", discs[0].ConfigTopic)
	}
}

// ---- DHWCircuit: composed water_heater --------------------------------------

func TestOnControllable_WaterHeaterComposition(t *testing.T) {
	m := NewMapper("eebus", "homeassistant")
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	m.OnManufacturer(&Manufacturer{Line: Line{SKI: ski}, BrandName: "SD", DeviceName: "VR920"})

	// First contribution: cdsf (operation mode select).
	modeLine := &Controllable{
		Line:       Line{SKI: ski, Entity: "3.1"},
		EntityType: "DHWCircuit",
		UseCase:    "cdsf",
		Component:  "select",
		Actions:    []string{"eco", "off", "on"},
		State:      "on",
	}
	discs := m.OnControllable(modeLine)
	if len(discs) != 1 {
		t.Fatalf("cdsf: expected 1 discovery, got %d", len(discs))
	}
	d := discs[0]
	if !strings.Contains(d.ConfigTopic, "/water_heater/eebus_bridge/") {
		t.Fatalf("config topic = %q, want a water_heater domain topic", d.ConfigTopic)
	}
	wh1, ok := d.Config.(*HAWaterHeater)
	if !ok {
		t.Fatalf("config is %T, want *HAWaterHeater", d.Config)
	}
	if len(wh1.Modes) != 3 {
		t.Errorf("modes = %v, want the 3 cdsf options", wh1.Modes)
	}
	if wh1.ModeCommandTopic != "eebus/"+ski+"/3_1/cdsf/mode/cmd" {
		t.Errorf("mode command topic = %q", wh1.ModeCommandTopic)
	}
	if wh1.CurrentTemperatureTopic != "eebus/"+ski+"/3_1/mdt/temperature/state" {
		t.Errorf("current temperature topic = %q", wh1.CurrentTemperatureTopic)
	}
	if wh1.ModeStateTopic != "eebus/"+ski+"/3_1/mdsf/operation_mode/state" {
		t.Errorf("mode state topic = %q", wh1.ModeStateTopic)
	}
	if len(d.CommandTopics) != 1 || d.CommandTopics[0] != wh1.ModeCommandTopic {
		t.Errorf("cdsf command topics = %v", d.CommandTopics)
	}
	// No cdt yet: no temperature command, no range.
	if wh1.TemperatureCommandTopic != "" || wh1.TemperatureStateTopic != "" {
		t.Errorf("temperature topics must be absent before cdt announces: %+v", wh1)
	}

	// Second contribution: cdt (target temperature number).
	tempLine := &Controllable{
		Line:       Line{SKI: ski, Entity: "3.1"},
		EntityType: "DHWCircuit",
		UseCase:    "cdt",
		Component:  "number",
		Unit:       "°C",
		Range:      &NumberRange{Min: 20, Max: f64(70), Step: 0.5},
		State:      "52",
	}
	discs = m.OnControllable(tempLine)
	if len(discs) != 1 {
		t.Fatalf("cdt: expected 1 discovery, got %d", len(discs))
	}
	d = discs[0]
	wh2, ok := d.Config.(*HAWaterHeater)
	if !ok {
		t.Fatalf("config is %T, want *HAWaterHeater", d.Config)
	}
	// The composite now carries BOTH contributions.
	if wh2.TemperatureCommandTopic != "eebus/"+ski+"/3_1/cdt/value/cmd" {
		t.Errorf("temperature command topic = %q", wh2.TemperatureCommandTopic)
	}
	if wh2.TemperatureStateTopic != "eebus/"+ski+"/3_1/cdt/value/state" {
		t.Errorf("temperature state topic = %q", wh2.TemperatureStateTopic)
	}
	if wh2.MinTemp == nil || *wh2.MinTemp != 20 || wh2.MaxTemp == nil || *wh2.MaxTemp != 70 {
		t.Errorf("min/max = (%v, %v), want (20, 70)", wh2.MinTemp, wh2.MaxTemp)
	}
	if wh2.Precision == nil || *wh2.Precision != 0.5 {
		t.Errorf("precision = %v, want 0.5", wh2.Precision)
	}
	if wh2.TemperatureUnit != "C" {
		t.Errorf("temperature unit = %q, want C", wh2.TemperatureUnit)
	}
	if len(wh2.Modes) != 3 {
		t.Errorf("modes must survive the cdt contribution, got %v", wh2.Modes)
	}
	// Only the cdt command topic is subscribed by this contribution.
	if len(d.CommandTopics) != 1 || d.CommandTopics[0] != wh2.TemperatureCommandTopic {
		t.Errorf("cdt command topics = %v", d.CommandTopics)
	}
	// The target temperature state refreshes.
	if d.StateTopic != wh2.TemperatureStateTopic || d.StateValue != "52" {
		t.Errorf("state = (%q, %q)", d.StateTopic, d.StateValue)
	}
}

func TestOnControllable_WaterHeaterNotForOtherEntityTypes(t *testing.T) {
	// A cdt/cdsf line on a NON-DHW entity type renders the standalone
	// components (number/select) — the composition is entity-type-driven, not
	// use-case-driven, so future HVAC-bearing entity types keep the generic
	// surface until they are explicitly composed.
	m := NewMapper("eebus", "homeassistant")
	c := &Controllable{
		Line:       Line{SKI: "ski1", Entity: "4.1"},
		EntityType: "SomethingElse",
		UseCase:    "cdsf",
		Component:  "select",
		Actions:    []string{"off", "on", "eco"},
	}
	discs := m.OnControllable(c)
	if len(discs) != 1 || !strings.Contains(discs[0].ConfigTopic, "/select/eebus_bridge/") {
		t.Fatalf("non-DHW cdsf must render a plain select, got %+v", discs)
	}
}

// ---- /mode/cmd command routing ----------------------------------------------

func TestDecodeHACommand_ModeCmd(t *testing.T) {
	c := &Controllable{Line: Line{SKI: "s", Entity: "2.1"}, UseCase: "crhsf", Component: "select"}
	op, val, unit, text, ok := decodeHACommand("eebus/s/2_1/crhsf/mode/cmd", "eco", c)
	if !ok {
		t.Fatal("decode failed")
	}
	if op != "crhsf.set" {
		t.Errorf("op = %q, want crhsf.set", op)
	}
	if text != "eco" {
		t.Errorf("text = %q, want eco", text)
	}
	if val != 0 || unit != "" {
		t.Errorf("val/unit = (%v, %q), want zero/empty", val, unit)
	}
}

func TestDecodeHACommand_ModeCmdSwitchPayloads(t *testing.T) {
	// The switch rendering publishes the same option strings ("on"/"off").
	c := &Controllable{Line: Line{SKI: "s", Entity: "2.1"}, UseCase: "crhsf"}
	for payload, want := range map[string]string{"on": "on", "off": "off"} {
		_, _, _, text, ok := decodeHACommand("eebus/s/2_1/crhsf/mode/cmd", payload, c)
		if !ok || text != want {
			t.Errorf("payload %q → (text=%q, ok=%v), want (%q, true)", payload, text, ok, want)
		}
	}
}

func TestDecodeHACommand_ModeCmdEmptyRejected(t *testing.T) {
	c := &Controllable{Line: Line{SKI: "s", Entity: "2.1"}, UseCase: "cdsf"}
	if _, _, _, _, ok := decodeHACommand("eebus/s/2_1/cdsf/mode/cmd", "   ", c); ok {
		t.Error("an empty mode payload must decode to ok=false")
	}
}

func TestCommandWireShapeWithText(t *testing.T) {
	// The NDJSON command line must carry the text payload for mode writes.
	c := &Controllable{Line: Line{SKI: "abc", Entity: "3.1"}, UseCase: "cdsf"}
	cmd := Command{
		Kind:   KindCommand,
		Op:     "cdsf.set",
		SKI:    c.SKI,
		Entity: c.Entity,
		Text:   "eco",
	}
	b, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, want := range []string{`"op":"cdsf.set"`, `"text":"eco"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
}

// ---- °C signals ---------------------------------------------------------------

func TestOnUcSignal_TemperatureDeviceClass(t *testing.T) {
	// An mdt temperature signal must map to a temperature-classed sensor with
	// the °C unit (it also feeds the water heater current temperature topic,
	// which is built deterministically from the same names).
	m := NewMapper("eebus", "homeassistant")
	sig := &UcSignal{
		Line:      Line{SKI: "ski1", Entity: "3.1"},
		UseCase:   "mdt",
		Signal:    "temperature",
		Value:     "48.5",
		ValueType: "number",
		Unit:      "°C",
	}
	disc := m.OnUcSignal(sig)
	sensor, ok := disc.Config.(*HASensor)
	if !ok {
		t.Fatalf("config is %T, want *HASensor", disc.Config)
	}
	if sensor.DeviceClass != "temperature" {
		t.Errorf("device class = %q, want temperature", sensor.DeviceClass)
	}
	if sensor.UnitOfMeasurement != "°C" {
		t.Errorf("unit = %q, want °C", sensor.UnitOfMeasurement)
	}
	if disc.StateTopic != "eebus/ski1/3_1/mdt/temperature/state" {
		t.Errorf("state topic = %q", disc.StateTopic)
	}
	if disc.StateValue != "48.5" {
		t.Errorf("state value = %q", disc.StateValue)
	}
}

func TestOnUcSignal_OperationModeStringSensor(t *testing.T) {
	m := NewMapper("eebus", "homeassistant")
	sig := &UcSignal{
		Line:      Line{SKI: "ski1", Entity: "2.1"},
		UseCase:   "mrhsf",
		Signal:    "operation_mode",
		Value:     "auto",
		ValueType: "string",
	}
	disc := m.OnUcSignal(sig)
	if disc.Config == nil {
		t.Fatal("expected a discovery config for a first-time signal")
	}
	if _, ok := disc.Config.(*HASensor); !ok {
		t.Fatalf("config is %T, want *HASensor", disc.Config)
	}
	if disc.StateValue != "auto" {
		t.Errorf("state value = %q", disc.StateValue)
	}
}

// f64 is a small helper for building NumberRange pointers in tests.
func f64(v float64) *float64 { return &v }
