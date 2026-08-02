// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Tests for the write-channel discovery path: OnControllable builds a climate
// entity for OHPCF, with the right command/state topics and preset modes.

package internal

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOnControllable_Climate_OHPCF(t *testing.T) {
	m := NewMapper("eebus", "homeassistant")
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	m.OnManufacturer(&Manufacturer{Line: Line{SKI: ski}, BrandName: "SD", DeviceName: "VR920"})

	c := &Controllable{
		Line:       Line{SKI: ski, Entity: "3.1"},
		EntityType: "Compressor",
		UseCase:    "ohpcf",
		Component:  "climate",
		Actions:    []string{"schedule", "pause", "resume", "abort"},
		State:      "running",
	}
	disc := m.OnControllable(c)
	if disc.Config == nil {
		t.Fatal("OnControllable must publish a discovery for climate")
	}
	cl, ok := disc.Config.(*HAClimate)
	if !ok {
		t.Fatalf("config payload is %T, want *HAClimate", disc.Config)
	}

	// Topic must be under climate/, not sensor/.
	if !strings.Contains(disc.ConfigTopic, "homeassistant/climate/eebus_bridge/") {
		t.Errorf("config topic should be climate/, got: %q", disc.ConfigTopic)
	}

	// Modes are the HA climate mode vocabulary for OHPCF.
	if len(cl.Modes) != 2 {
		t.Errorf("Modes = %v, want [off auto]", cl.Modes)
	}
	for _, want := range []string{"off", "auto"} {
		found := false
		for _, mm := range cl.Modes {
			if mm == want {
				found = true
			}
		}
		if !found {
			t.Errorf("Modes missing %q (got %v)", want, cl.Modes)
		}
	}

	// Presets derived from the actions list: pause + resume must be present.
	for _, want := range []string{"pause", "resume"} {
		found := false
		for _, p := range cl.PresetModes {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Errorf("PresetModes missing %q (got %v)", want, cl.PresetModes)
		}
	}

	// Command topics must end with /mode/cmd and /preset/cmd so the
	// orchestrator's decodeHACommand suffix match works.
	if !strings.HasSuffix(cl.ModeCommandTopic, "/ohpcf/mode/cmd") {
		t.Errorf("mode command topic wrong: %q", cl.ModeCommandTopic)
	}
	if !strings.HasSuffix(cl.PresetModeCommandTopic, "/ohpcf/preset/cmd") {
		t.Errorf("preset command topic wrong: %q", cl.PresetModeCommandTopic)
	}

	// Both command topics must be in Discovery.CommandTopics (for subscription).
	if len(disc.CommandTopics) != 2 {
		t.Errorf("CommandTopics = %v, want 2 entries", disc.CommandTopics)
	}

	// Device block must be attached (same device as sensors).
	if cl.Device == nil || cl.Device.Name != "VR920" {
		t.Errorf("device not attached: %+v", cl.Device)
	}

	// Initial state: action "running" → mode "auto" (climateModeFromHAAction).
	if disc.StateValue != "auto" {
		t.Errorf("initial mode state = %q, want auto (action was running)", disc.StateValue)
	}
	if disc.ActionValue != "running" {
		t.Errorf("action value = %q, want running", disc.ActionValue)
	}
}

func TestOnControllable_Climate_StateOffYieldsModeOff(t *testing.T) {
	m := NewMapper("eebus", "homeassistant")
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	c := &Controllable{
		Line:      Line{SKI: ski, Entity: "3.1"},
		UseCase:   "ohpcf",
		Component: "climate",
		Actions:   []string{"abort"},
		State:     "off",
	}
	disc := m.OnControllable(c)
	if disc.StateValue != "off" {
		t.Errorf("mode state = %q, want off", disc.StateValue)
	}
	// No pause/resume action → no presets.
	cl := disc.Config.(*HAClimate)
	if len(cl.PresetModes) != 0 {
		t.Errorf("PresetModes should be empty without pause/resume, got %v", cl.PresetModes)
	}
}

func TestOnControllable_UnknownComponentNoDiscovery(t *testing.T) {
	// A future use case might expose a component we do not model yet (e.g.
	// "select"). Until we wire it here, OnControllable MUST return an empty
	// discovery (no config) rather than crashing or emitting a half-formed
	// payload.
	m := NewMapper("eebus", "homeassistant")
	c := &Controllable{
		Line:      Line{SKI: "ski", Entity: "0"},
		UseCase:   "futureselect",
		Component: "select",
		Actions:   []string{"pick"},
	}
	disc := m.OnControllable(c)
	if disc.Config != nil {
		t.Errorf("unknown component should yield no discovery, got %T", disc.Config)
	}
}

func TestOnControllable_DedupedByUniqueID(t *testing.T) {
	// Same (ski, entity, usecase) must publish discovery only once.
	m := NewMapper("eebus", "homeassistant")
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	c := &Controllable{
		Line:      Line{SKI: ski, Entity: "3.1"},
		UseCase:   "ohpcf",
		Component: "climate",
		Actions:   []string{"pause"},
		State:     "running",
	}
	first := m.OnControllable(c)
	if first.Config == nil {
		t.Fatal("first call must publish")
	}
	second := m.OnControllable(c)
	if second.Config != nil {
		t.Error("second call must NOT re-publish discovery (already announced)")
	}
}

// TestOnControllable_Climate_StateRefreshUpdatesAction is the regression guard
// for the OHPCF state-refresh fix. After the first announcement, the daemon
// keeps emitting controllable lines as the compressor transitions (running →
// paused → …); OnControllable must NOT re-publish discovery, but it MUST
// return a fresh action/state pair so the orchestrator can republish the
// climate entity's action topic on each refresh. Previously the bridge
// computed these fields and then the orchestrator dropped them; this test
// pins the mapper half (the orchestrator half is covered separately).
func TestOnControllable_Climate_StateRefreshUpdatesAction(t *testing.T) {
	m := NewMapper("eebus", "homeassistant")
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	c := &Controllable{
		Line:      Line{SKI: ski, Entity: "3.1"},
		UseCase:   "ohpcf",
		Component: "climate",
		Actions:   []string{"schedule", "pause", "resume"},
		State:     "running",
	}
	// First call: discovery + initial action "running" → mode "auto".
	first := m.OnControllable(c)
	if first.Config == nil {
		t.Fatal("first call must publish discovery")
	}
	if first.ActionValue != "running" {
		t.Fatalf("initial action = %q, want running", first.ActionValue)
	}
	if first.StateValue != "auto" {
		t.Fatalf("initial mode = %q, want auto", first.StateValue)
	}

	// Refresh: compressor transitioned running → paused. The mapper must
	// surface the new action without re-publishing discovery.
	c.State = "idle"
	second := m.OnControllable(c)
	if second.Config != nil {
		t.Error("refresh must NOT re-publish discovery")
	}
	if second.ActionValue != "idle" {
		t.Errorf("refresh action = %q, want idle", second.ActionValue)
	}
	if second.StateValue != "auto" {
		t.Errorf("refresh mode = %q, want auto (action idle is non-off)", second.StateValue)
	}
	if second.ActionTopic == "" || second.ActionTopic != first.ActionTopic {
		t.Errorf("refresh action topic changed: first=%q second=%q", first.ActionTopic, second.ActionTopic)
	}

	// Second refresh: compressor stopped → action "off". mode/state must
	// follow (climateModeFromHAAction maps "off" → "off").
	c.State = "off"
	third := m.OnControllable(c)
	if third.Config != nil {
		t.Error("third call must NOT re-publish discovery")
	}
	if third.ActionValue != "off" {
		t.Errorf("third action = %q, want off", third.ActionValue)
	}
	if third.StateValue != "off" {
		t.Errorf("third mode = %q, want off", third.StateValue)
	}
}

func TestDecodeHACommand_ModeOff(t *testing.T) {
	c := &Controllable{Line: Line{SKI: "s", Entity: "3.1"}, UseCase: "ohpcf"}
	op, val, unit, ok := decodeHACommand("eebus/s/3_1/ohpcf/mode/cmd", "off", c)
	if !ok {
		t.Fatal("decode failed")
	}
	if op != "ohpcf.abort" {
		t.Errorf("op = %q, want ohpcf.abort", op)
	}
	_ = val
	_ = unit
}

func TestDecodeHACommand_ModeAuto(t *testing.T) {
	c := &Controllable{Line: Line{SKI: "s", Entity: "3.1"}, UseCase: "ohpcf"}
	op, val, unit, ok := decodeHACommand("eebus/s/3_1/ohpcf/mode/cmd", "auto", c)
	if !ok {
		t.Fatal("decode failed")
	}
	if op != "ohpcf.schedule" {
		t.Errorf("op = %q, want ohpcf.schedule", op)
	}
	if unit != "seconds" {
		t.Errorf("unit = %q, want seconds", unit)
	}
	if val != 0 {
		t.Errorf("value = %v, want 0", val)
	}
}

func TestDecodeHACommand_PresetPause(t *testing.T) {
	c := &Controllable{Line: Line{SKI: "s", Entity: "3.1"}, UseCase: "ohpcf"}
	op, _, _, ok := decodeHACommand("eebus/s/3_1/ohpcf/preset/cmd", "pause", c)
	if !ok || op != "ohpcf.pause" {
		t.Errorf("pause → %q ok=%v, want ohpcf.pause", op, ok)
	}
	op2, _, _, ok2 := decodeHACommand("eebus/s/3_1/ohpcf/preset/cmd", "resume", c)
	if !ok2 || op2 != "ohpcf.resume" {
		t.Errorf("resume → %q ok=%v, want ohpcf.resume", op2, ok2)
	}
}

func TestDecodeHACommand_UnknownPayload(t *testing.T) {
	c := &Controllable{Line: Line{SKI: "s", Entity: "3.1"}, UseCase: "ohpcf"}
	_, _, _, ok := decodeHACommand("eebus/s/3_1/ohpcf/mode/cmd", "ecOmode", c)
	if ok {
		t.Error("unknown payload must decode to ok=false so the orchestrator logs+drops it")
	}
}

func TestOnControllable_Number_LPC(t *testing.T) {
	// LPC exposes a number entity (power limit in watts). Verify the discovery
	// payload, topic layout, command topic subscription and the optional
	// min/max/step range propagated from c.Range.
	m := NewMapper("eebus", "homeassistant")
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	m.OnManufacturer(&Manufacturer{Line: Line{SKI: ski}, BrandName: "SD", DeviceName: "VR920"})

	maxV := 8000.0
	c := &Controllable{
		Line:       Line{SKI: ski, Entity: "1.1"},
		EntityType: "HeatPumpAppliance",
		UseCase:    "lpc",
		Component:  "number",
		Unit:       "W",
		Range: &NumberRange{
			Min:  0,
			Max:  &maxV,
			Step: 1,
		},
		Actions: []string{"set", "clear"},
		State:   "1500",
	}
	disc := m.OnControllable(c)
	if disc.Config == nil {
		t.Fatal("OnControllable must publish a discovery for number")
	}
	nb, ok := disc.Config.(*HANumber)
	if !ok {
		t.Fatalf("config payload is %T, want *HANumber", disc.Config)
	}

	// Topic must be under number/, not sensor/ or climate/.
	if !strings.Contains(disc.ConfigTopic, "homeassistant/number/eebus_bridge/") {
		t.Errorf("config topic should be number/, got: %q", disc.ConfigTopic)
	}

	// Unit must be propagated from the controllable line.
	if nb.UnitOfMeasurement != "W" {
		t.Errorf("unit = %q, want W", nb.UnitOfMeasurement)
	}

	// Command topic must end with /value/cmd so decodeHACommand routes it.
	if !strings.HasSuffix(nb.CommandTopic, "/lpc/value/cmd") {
		t.Errorf("command topic wrong: %q", nb.CommandTopic)
	}

	// One command topic to subscribe to (the value cmd).
	if len(disc.CommandTopics) != 1 || disc.CommandTopics[0] != nb.CommandTopic {
		t.Errorf("CommandTopics = %v, want [%s]", disc.CommandTopics, nb.CommandTopic)
	}

	// Device block attached.
	if nb.Device == nil || nb.Device.Name != "VR920" {
		t.Errorf("device not attached: %+v", nb.Device)
	}

	// Initial state passes through verbatim (the watts value).
	if disc.StateValue != "1500" {
		t.Errorf("state value = %q, want 1500", disc.StateValue)
	}

	// Range must be propagated: min/max/step from c.Range.
	if nb.Min == nil || *nb.Min != 0 {
		t.Errorf("Min = %v, want 0", nb.Min)
	}
	if nb.Max == nil || *nb.Max != 8000 {
		t.Errorf("Max = %v, want 8000", nb.Max)
	}
	if nb.Step == nil || *nb.Step != 1 {
		t.Errorf("Step = %v, want 1", nb.Step)
	}
}

func TestOnControllable_Number_NoRange(t *testing.T) {
	// When c.Range is nil (device did not advertise a ceiling), the number MUST
	// be published without min/max/step so HA does not fall back to its default
	// cap of 100. This is the VR920 case: nominal_max is not exposed.
	m := NewMapper("eebus", "homeassistant")
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	c := &Controllable{
		Line:      Line{SKI: ski, Entity: "1.1"},
		UseCase:   "lpc",
		Component: "number",
		Unit:      "W",
		Actions:   []string{"set"},
		State:     "1500",
		// Range intentionally nil.
	}
	disc := m.OnControllable(c)
	if disc.Config == nil {
		t.Fatal("OnControllable must publish a discovery for number")
	}
	nb := disc.Config.(*HANumber)
	if nb.Min != nil || nb.Max != nil || nb.Step != nil {
		t.Errorf("range fields must be nil when c.Range is nil, got Min=%v Max=%v Step=%v", nb.Min, nb.Max, nb.Step)
	}
}

func TestOnControllable_Number_StateRefreshNoReannounce(t *testing.T) {
	// A second controllable line for the same (ski, entity, usecase) must NOT
	// re-publish discovery; it must only refresh the state topic.
	m := NewMapper("eebus", "homeassistant")
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	c := &Controllable{
		Line:      Line{SKI: ski, Entity: "1.1"},
		UseCase:   "lpc",
		Component: "number",
		Unit:      "W",
		Actions:   []string{"set"},
		State:     "1500",
	}
	first := m.OnControllable(c)
	if first.Config == nil {
		t.Fatal("first call must publish")
	}
	// Refresh with a new limit value.
	c.State = "2000"
	second := m.OnControllable(c)
	if second.Config != nil {
		t.Error("second call must NOT re-publish discovery")
	}
	if second.StateValue != "2000" {
		t.Errorf("refresh state = %q, want 2000", second.StateValue)
	}
	if !strings.HasSuffix(second.StateTopic, "/lpc/value/state") {
		t.Errorf("refresh topic wrong: %q", second.StateTopic)
	}
}

func TestDecodeHACommand_ValueSet(t *testing.T) {
	c := &Controllable{Line: Line{SKI: "s", Entity: "1.1"}, UseCase: "lpc", Component: "number", Unit: "W"}
	op, val, unit, ok := decodeHACommand("eebus/s/1_1/lpc/value/cmd", "1500", c)
	if !ok {
		t.Fatal("decode failed")
	}
	if op != "lpc.set" {
		t.Errorf("op = %q, want lpc.set", op)
	}
	if val != 1500 {
		t.Errorf("value = %v, want 1500", val)
	}
	if unit != "W" {
		t.Errorf("unit = %q, want W", unit)
	}
}

func TestDecodeHACommand_ValueFractional(t *testing.T) {
	c := &Controllable{Line: Line{SKI: "s", Entity: "1.1"}, UseCase: "lpc", Component: "number", Unit: "W"}
	_, val, _, ok := decodeHACommand("eebus/s/1_1/lpc/value/cmd", "750.5", c)
	if !ok {
		t.Fatal("decode failed for fractional value")
	}
	if val != 750.5 {
		t.Errorf("value = %v, want 750.5", val)
	}
}

func TestDecodeHACommand_ValueInvalid(t *testing.T) {
	// A non-numeric payload must decode to ok=false so the orchestrator logs
	// and drops it rather than sending a garbage value to eebusd.
	c := &Controllable{Line: Line{SKI: "s", Entity: "1.1"}, UseCase: "lpc", Component: "number"}
	_, _, _, ok := decodeHACommand("eebus/s/1_1/lpc/value/cmd", "not-a-number", c)
	if ok {
		t.Error("non-numeric payload must decode to ok=false")
	}
}

// TestCommandWireShape verifies the NDJSON line the bridge writes to eebusd's
// stdin matches what the eebusd dispatcher expects (kind=command, op, ski,
// entity). This is the wire contract between the two binaries.
func TestCommandWireShape(t *testing.T) {
	c := &Controllable{Line: Line{SKI: "abc", Entity: "3.1"}, UseCase: "ohpcf"}
	op, val, unit, ok := decodeHACommand("eebus/abc/3_1/ohpcf/preset/cmd", "pause", c)
	if !ok {
		t.Fatal("decode failed")
	}
	cmd := Command{
		Kind:   KindCommand,
		Op:     op,
		SKI:    c.SKI,
		Entity: c.Entity,
		Value:  val,
		Unit:   unit,
	}
	b, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		`"kind":"command"`,
		`"op":"ohpcf.pause"`,
		`"ski":"abc"`,
		`"entity":"3.1"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
}
