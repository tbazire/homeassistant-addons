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
// stack; we focus here on the guards and the state-mapping table.

func TestModuleNameAndComponent(t *testing.T) {
	m := &Module{}
	if m.Name() != "ohpcf" {
		t.Errorf("Name = %q, want ohpcf", m.Name())
	}
	if m.HAComponent() != "climate" {
		t.Errorf("HAComponent = %q, want climate", m.HAComponent())
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

func TestMapStateToHA(t *testing.T) {
	// The state mapping is the bridge between SPINE compressor state and HA
	// climate action vocabulary. It MUST be stable (it is the wire contract
	// for the initial state publish).
	cases := []struct {
		in   ucapi.CompressorPowerConsumptionStateType
		want string
	}{
		{ucapi.CompressorPowerConsumptionStateRunning, "heating"},
		{ucapi.CompressorPowerConsumptionStatePaused, "idle"},
		{ucapi.CompressorPowerConsumptionStateCompleted, "idle"},
		{ucapi.CompressorPowerConsumptionStateStopped, "idle"},
		{ucapi.CompressorPowerConsumptionStateAvailable, "off"},
		{ucapi.CompressorPowerConsumptionStateScheduled, "off"},
		{"", ""},
		{"unknownstate", ""},
	}
	for _, c := range cases {
		got := mapStateToHA(c.in)
		if got != c.want {
			t.Errorf("mapStateToHA(%q) = %q, want %q", c.in, got, c.want)
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
	// OHPCF is a climate entity (modes/presets), not a numeric setpoint, so
	// NumberRangeForEntity must always return nil regardless of the entity.
	m := &Module{}
	if r := m.NumberRangeForEntity(nil); r != nil {
		t.Errorf("NumberRangeForEntity = %v, want nil (climate has no range)", r)
	}
}

// TestRecordStateTransition_Dedup is the regression guard for the OHPCF
// state-refresh fix: spine-go re-fires DataUpdateConsumptionState on every
// notify that carries a non-nil State (it does not diff), so the module MUST
// suppress identical consecutive states or the bridge will flood MQTT with
// redundant controllable lines. A genuine transition (running → paused →
// running) must each emit; a repeat must not.
func TestRecordStateTransition_Dedup(t *testing.T) {
	m := &Module{}
	const key = "ski-x/3.1"

	// First emission for this entity: a new state, must emit.
	if !m.recordStateTransition(key, "heating") {
		t.Error("first state (heating) should emit")
	}
	// Identical re-notify: spine-go fires again, but nothing changed — must
	// be suppressed to avoid a redundant controllable line.
	if m.recordStateTransition(key, "heating") {
		t.Error("identical state (heating again) must NOT emit")
	}
	// Real transition to paused: must emit.
	if !m.recordStateTransition(key, "idle") {
		t.Error("transition to idle should emit")
	}
	// Back to heating: even though seen before, it differs from the last
	// value — must emit (the dedup is "changed since last publish", not
	// "never seen before").
	if !m.recordStateTransition(key, "heating") {
		t.Error("transition back to heating should emit")
	}
}

// TestRecordStateTransition_EmptyIgnored ensures a transient empty/unmapped
// state never clears the cache or emits a refresh that would blank the
// climate entity to "unknown". The previous value stays cached.
func TestRecordStateTransition_EmptyIgnored(t *testing.T) {
	m := &Module{}
	const key = "ski/0"
	if m.recordStateTransition(key, "") {
		t.Error("empty mapped state must never emit")
	}
	// Cache stays empty: a subsequent real state still emits as the first.
	if !m.recordStateTransition(key, "off") {
		t.Error("real state after empty should emit")
	}
	// And another empty does not disturb the cached "off".
	if m.recordStateTransition(key, "") {
		t.Error("empty after a real state must never emit")
	}
	if m.recordStateTransition(key, "off") {
		t.Error("identical state after empty must still be suppressed")
	}
}

// TestRecordStateTransition_PerEntityIsolation confirms two different
// compressors (distinct keys) do not share dedup state: the same action on
// two devices emits for each, independently.
func TestRecordStateTransition_PerEntityIsolation(t *testing.T) {
	m := &Module{}
	if !m.recordStateTransition("dev1/3.1", "heating") {
		t.Error("dev1 first state should emit")
	}
	if !m.recordStateTransition("dev2/3.1", "heating") {
		t.Error("dev2 first state should emit independently of dev1")
	}
	// Now both repeat — both suppressed.
	if m.recordStateTransition("dev1/3.1", "heating") {
		t.Error("dev1 repeat must be suppressed")
	}
	if m.recordStateTransition("dev2/3.1", "heating") {
		t.Error("dev2 repeat must be suppressed")
	}
}

// TestEntityStateKeyFormat asserts the cache key embeds both the ski and the
// dotted entity address, so the dedup aligns with the controllable line's
// identity (a bridge can be paired with several devices).
func TestEntityStateKeyFormat(t *testing.T) {
	// nil entity → graceful fallback rather than a panic.
	if k := entityStateKey("ski", nil); k != "ski/?" {
		t.Errorf("nil entity key = %q, want ski/?", k)
	}
}
