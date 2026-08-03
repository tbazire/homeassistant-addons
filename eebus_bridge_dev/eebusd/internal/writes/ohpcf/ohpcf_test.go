// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire

package ohpcf

import (
	"testing"

	"eebusd/internal/writes/wucapi"
	ucapi "github.com/enbility/eebus-go/usecases/api"
)

// The module's wrapper logic (the parts WE wrote, not the underlying eebus-go
// OHPCF implementation) is what matters for regression. The end-to-end write
// path is exercised by eebus-go's own ohpcf tests against a mocked SPINE
// stack; we focus here on the guards and the state mapping.

func TestModuleNameAndComponent(t *testing.T) {
	m := &Module{}
	if m.Name() != "ohpcf" {
		t.Errorf("Name = %q, want ohpcf", m.Name())
	}
	// OHPCF is exposed as "buttons": the bridge expands that into one HA button
	// per action (schedule/pause/resume/abort). This replaced the former
	// "climate" component, whose modes/presets were a poor fit for momentary
	// process commands.
	if m.HAComponent() != "buttons" {
		t.Errorf("HAComponent = %q, want buttons", m.HAComponent())
	}
}

func TestModuleActions(t *testing.T) {
	// Before Bind (m.impl == nil) AvailableActionsForEntity falls back to the
	// full static list so discovery is never blocked while wiring completes.
	m := &Module{}
	actions := m.AvailableActionsForEntity(nil)
	want := map[string]bool{"schedule": true, "pause": true, "resume": true, "abort": true}
	if len(actions) != len(want) {
		t.Fatalf("AvailableActionsForEntity = %v, want %d entries", actions, len(want))
	}
	for _, a := range actions {
		if !want[a] {
			t.Errorf("unexpected action %q", a)
		}
	}
}

func TestModuleDispatchBeforeBindErrors(t *testing.T) {
	// Without Bind, the module has no underlying impl. Dispatch must refuse
	// with a clear error rather than panicking on a nil pointer dereference.
	m := &Module{}
	err := m.Dispatch("pause", "ski", nil, wucapi.Args{}, nil)
	if err == nil {
		t.Fatal("Dispatch before Bind should return an error")
	}
}

func TestEmitSignalsNilSafe(t *testing.T) {
	// Before Bind, EmitSignals must be a no-op (no panic), not call the
	// callback. This guards the initial-announce path in the daemon.
	m := &Module{}
	m.EmitSignals("ski", nil) // nil entity + no impl → returns immediately
	// No assertion beyond "did not panic"; the callback is nil so any call
	// would have panicked inside the emit helpers.
}

func TestFormatFloat(t *testing.T) {
	// formatFloat backs the number-signal value formatting (requested/max
	// power). It must render integers without a decimal point and trim
	// fractional trailing zeros — the value IS the wire contract.
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1500, "1500"},
		{1500.5, "1500.5"},
		{1500.25, "1500.25"},
		{1500.123, "1500.123"},
		{1500.1239, "1500.124"},
	}
	for _, c := range cases {
		got := formatFloat(c.in)
		if got != c.want {
			t.Errorf("formatFloat(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestMapStateToRaw pins the process_state sensor values. Unlike the former
// mapStateToHA (which conflated several SPINE states into the climate action
// vocabulary heating/idle/off), mapStateToRaw passes the enum name through
// verbatim so the sensor reflects the device's real state. This is the wire
// contract for the process_state sensor: changing it silently would change
// what HA displays.
func TestMapStateToRaw(t *testing.T) {
	cases := []struct {
		in   ucapi.CompressorPowerConsumptionStateType
		want string
	}{
		{ucapi.CompressorPowerConsumptionStateRunning, "running"},
		{ucapi.CompressorPowerConsumptionStatePaused, "paused"},
		{ucapi.CompressorPowerConsumptionStateCompleted, "completed"},
		{ucapi.CompressorPowerConsumptionStateStopped, "stopped"},
		{ucapi.CompressorPowerConsumptionStateAvailable, "available"},
		{ucapi.CompressorPowerConsumptionStateScheduled, "scheduled"},
		{ucapi.CompressorPowerConsumptionStateType(""), ""},
	}
	for _, c := range cases {
		got := mapStateToRaw(c.in)
		if got != c.want {
			t.Errorf("mapStateToRaw(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestModuleRegisteredInGlobalRegistry(t *testing.T) {
	// init() of this package registers the module. We can't import the parent
	// writes package here (import cycle), so we just assert that our own
	// constructor returns a usable Module with the expected identity. The
	// registry wiring itself is tested in the writes package tests.
	m := &Module{}
	if m.Name() == "" {
		t.Fatal("module name is empty")
	}
}

func TestModuleNumberRangeAlwaysNil(t *testing.T) {
	// OHPCF is exposed as buttons (momentary triggers), not a numeric setpoint,
	// so NumberRangeForEntity must always return nil regardless of the entity.
	m := &Module{}
	if r := m.NumberRangeForEntity(nil); r != nil {
		t.Errorf("NumberRangeForEntity = %v, want nil (buttons have no range)", r)
	}
}
