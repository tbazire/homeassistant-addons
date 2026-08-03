// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Tests for the write-channel discovery path:
//   - OnControllable builds a HA button per OHPCF action (schedule/pause/resume/
//     abort), filtered by device capability.
//   - OnControllable builds a single HA number for LPC (power limit).
//   - decodeHACommand maps button topics and value topics to eebusd ops.

package internal

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---- OHPCF buttons ---------------------------------------------------------

// TestOnControllable_Buttons_OHPCF is the core test for the OHPCF buttons
// component: one controllable line with N actions must expand into N HA button
// discoveries, each with a distinct unique_id and a /btn/<action>/cmd command
// topic. The actions are already capability-filtered by the daemon, so the
// bridge trusts the list it receives.
func TestOnControllable_Buttons_OHPCF(t *testing.T) {
	m := NewMapper("eebus", "homeassistant")
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	m.OnManufacturer(&Manufacturer{Line: Line{SKI: ski}, BrandName: "SD", DeviceName: "VR920"})

	c := &Controllable{
		Line:       Line{SKI: ski, Entity: "3.1"},
		EntityType: "Compressor",
		UseCase:    "ohpcf",
		Component:  "buttons",
		Actions:    []string{"schedule", "pause", "resume", "abort"},
	}
	discs := m.OnControllable(c)
	if len(discs) != 4 {
		t.Fatalf("got %d discoveries, want 4 (one button per action)", len(discs))
	}

	seen := map[string]*HAButton{}
	for _, d := range discs {
		btn, ok := d.Config.(*HAButton)
		if !ok {
			t.Fatalf("config payload is %T, want *HAButton", d.Config)
		}
		// Each button discovery must live under button/ in HA discovery.
		if !strings.Contains(d.ConfigTopic, "homeassistant/button/eebus_bridge/") {
			t.Errorf("config topic should be button/, got: %q", d.ConfigTopic)
		}
		// Exactly one command topic, and it is the button's own.
		if len(d.CommandTopics) != 1 || d.CommandTopics[0] != btn.CommandTopic {
			t.Errorf("CommandTopics = %v, want [%s]", d.CommandTopics, btn.CommandTopic)
		}
		// Device block attached (same device as sensors).
		if btn.Device == nil || btn.Device.Name != "VR920" {
			t.Errorf("device not attached: %+v", btn.Device)
		}
		// Unique ids must be distinct across the four buttons.
		if _, dup := seen[btn.UniqueID]; dup {
			t.Errorf("duplicate unique_id %q", btn.UniqueID)
		}
		seen[btn.UniqueID] = btn
	}

	// Each action must have produced exactly one button whose command topic
	// ends with /btn/<action>/cmd — that suffix is what decodeHACommand keys
	// on to route the press back to <uc>.<action>.
	for _, action := range c.Actions {
		found := false
		for _, btn := range seen {
			if strings.HasSuffix(btn.CommandTopic, "/ohpcf/btn/"+action+"/cmd") {
				found = true
			}
		}
		if !found {
			t.Errorf("no button with command topic ending /ohpcf/btn/%s/cmd", action)
		}
	}
}

// TestOnControllable_Buttons_FilteredByCapability confirms the bridge renders
// exactly the actions the daemon sent — no more, no less. The daemon already
// filtered pause/abort by is_pausable/is_stoppable, so [schedule, abort] yields
// exactly two buttons (pause/resume absent). This is the security-relevant
// property: a button the device cannot honour is never exposed.
func TestOnControllable_Buttons_FilteredByCapability(t *testing.T) {
	m := NewMapper("eebus", "homeassistant")
	c := &Controllable{
		Line:      Line{SKI: "ski", Entity: "3.1"},
		UseCase:   "ohpcf",
		Component: "buttons",
		Actions:   []string{"schedule", "abort"}, // device is not pausable
	}
	discs := m.OnControllable(c)
	if len(discs) != 2 {
		t.Fatalf("got %d buttons, want 2 (schedule + abort only)", len(discs))
	}
	for _, d := range discs {
		btn := d.Config.(*HAButton)
		if strings.Contains(btn.CommandTopic, "/btn/pause/") || strings.Contains(btn.CommandTopic, "/btn/resume/") {
			t.Errorf("pause/resume button should not exist for a non-pausable device: %s", btn.CommandTopic)
		}
	}
}

// TestOnControllable_Buttons_NoState confirms buttons carry no state topic: a
// button is a momentary trigger, and the process state lives in a separate
// sensor fed by uc_signal. None of the per-button discoveries may set
// StateTopic/StateValue/ActionTopic.
func TestOnControllable_Buttons_NoState(t *testing.T) {
	m := NewMapper("eebus", "homeassistant")
	c := &Controllable{
		Line:      Line{SKI: "ski", Entity: "3.1"},
		UseCase:   "ohpcf",
		Component: "buttons",
		Actions:   []string{"schedule"},
		State:     "running", // state is carried by the sensor, not the buttons
	}
	for _, d := range m.OnControllable(c) {
		if d.StateTopic != "" || d.StateValue != "" || d.ActionTopic != "" || d.ActionValue != "" {
			t.Errorf("button must carry no state, got StateTopic=%q StateValue=%q ActionTopic=%q ActionValue=%q",
				d.StateTopic, d.StateValue, d.ActionTopic, d.ActionValue)
		}
	}
}

// TestOnControllable_Buttons_Dedup confirms a second controllable line for the
// same (ski, entity, usecase) does NOT re-publish the buttons. The composite
// use-case id is marked announced, so a refresh returns nil (buttons have no
// state to refresh).
func TestOnControllable_Buttons_Dedup(t *testing.T) {
	m := NewMapper("eebus", "homeassistant")
	c := &Controllable{
		Line:      Line{SKI: "ski", Entity: "3.1"},
		UseCase:   "ohpcf",
		Component: "buttons",
		Actions:   []string{"schedule", "pause"},
	}
	if len(m.OnControllable(c)) != 2 {
		t.Fatal("first call must publish 2 buttons")
	}
	// Second call: already announced → nothing to refresh (buttons have no state).
	if discs := m.OnControllable(c); len(discs) != 0 {
		t.Errorf("second call must return no discoveries (buttons have no state refresh), got %d", len(discs))
	}
}

// TestOnControllable_UnknownComponentNoDiscovery: a component we do not model
// (e.g. "select") must yield no discovery rather than crashing or emitting a
// half-formed payload.
func TestOnControllable_UnknownComponentNoDiscovery(t *testing.T) {
	m := NewMapper("eebus", "homeassistant")
	c := &Controllable{
		Line:      Line{SKI: "ski", Entity: "0"},
		UseCase:   "futureselect",
		Component: "select",
		Actions:   []string{"pick"},
	}
	if discs := m.OnControllable(c); len(discs) != 0 {
		t.Errorf("unknown component should yield no discovery, got %d", len(discs))
	}
}

// ---- LPC number (unchanged semantics, adapted to []Discovery) -------------

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
	discs := m.OnControllable(c)
	if len(discs) != 1 {
		t.Fatalf("number must yield exactly 1 discovery, got %d", len(discs))
	}
	disc := discs[0]
	if disc.Config == nil {
		t.Fatal("OnControllable must publish a discovery for number")
	}
	nb, ok := disc.Config.(*HANumber)
	if !ok {
		t.Fatalf("config payload is %T, want *HANumber", disc.Config)
	}

	// Topic must be under number/, not sensor/ or button/.
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
	disc := m.OnControllable(c)[0]
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
	if m.OnControllable(c)[0].Config == nil {
		t.Fatal("first call must publish")
	}
	// Refresh with a new limit value.
	c.State = "2000"
	second := m.OnControllable(c)[0]
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

// ---- decodeHACommand: buttons ---------------------------------------------

func TestDecodeHACommand_ButtonActions(t *testing.T) {
	c := &Controllable{Line: Line{SKI: "s", Entity: "3.1"}, UseCase: "ohpcf"}
	cases := []struct {
		action string
		wantOp string
	}{
		{"schedule", "ohpcf.schedule"},
		{"pause", "ohpcf.pause"},
		{"resume", "ohpcf.resume"},
		{"abort", "ohpcf.abort"},
	}
	for _, tc := range cases {
		topic := "eebus/s/3_1/ohpcf/btn/" + tc.action + "/cmd"
		op, val, unit, ok := decodeHACommand(topic, "PRESS", c)
		if !ok {
			t.Errorf("%s: decode failed", tc.action)
			continue
		}
		if op != tc.wantOp {
			t.Errorf("%s: op = %q, want %q", tc.action, op, tc.wantOp)
		}
		// schedule carries a start-delay arg (unit seconds, value 0 = now);
		// the others carry no payload.
		if tc.action == "schedule" {
			if unit != "seconds" || val != 0 {
				t.Errorf("schedule: val=%v unit=%q, want 0 seconds", val, unit)
			}
		} else if val != 0 || unit != "" {
			t.Errorf("%s: val=%v unit=%q, want 0/empty (no payload)", tc.action, val, unit)
		}
	}
}

func TestDecodeHACommand_ButtonIgnoresPayload(t *testing.T) {
	// A button press carries "PRESS" from HA, but the action is determined by
	// the topic, not the payload. Any payload must still decode the same op.
	c := &Controllable{Line: Line{SKI: "s", Entity: "3.1"}, UseCase: "ohpcf"}
	for _, payload := range []string{"PRESS", "", "anything"} {
		op, _, _, ok := decodeHACommand("eebus/s/3_1/ohpcf/btn/pause/cmd", payload, c)
		if !ok || op != "ohpcf.pause" {
			t.Errorf("payload %q → op=%q ok=%v, want ohpcf.pause", payload, op, ok)
		}
	}
}

// ---- decodeHACommand: number (LPC) ----------------------------------------

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
// entity). This is the wire contract between the two binaries. Uses a button
// press as the representative command.
func TestCommandWireShape(t *testing.T) {
	c := &Controllable{Line: Line{SKI: "abc", Entity: "3.1"}, UseCase: "ohpcf"}
	op, val, unit, ok := decodeHACommand("eebus/abc/3_1/ohpcf/btn/pause/cmd", "PRESS", c)
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
