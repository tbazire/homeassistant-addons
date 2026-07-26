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
	// A future use case might expose a "number" component. Until we wire it
	// here, OnControllable MUST return an empty discovery (no config) rather
	// than crashing or emitting a half-formed payload.
	m := NewMapper("eebus", "homeassistant")
	c := &Controllable{
		Line:      Line{SKI: "ski", Entity: "0"},
		UseCase:   "lpc",
		Component: "number",
		Actions:   []string{"set"},
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
