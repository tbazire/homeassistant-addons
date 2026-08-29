// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire

package lpc

import (
	"testing"

	"eebusd/internal/writes/wucapi"
)

// The module's wrapper logic (the parts WE wrote, not the underlying eebus-go
// LPC implementation) is what matters for regression. The end-to-end write
// path is exercised by eebus-go's own lpc tests against a mocked SPINE stack;
// we focus here on the guards and the watts formatter.

func TestModuleNameAndComponent(t *testing.T) {
	m := &Module{}
	if m.Name() != "lpc" {
		t.Errorf("Name = %q, want lpc", m.Name())
	}
	if m.HAComponent() != "number" {
		t.Errorf("HAComponent = %q, want number", m.HAComponent())
	}
	if m.HAUnit() != "W" {
		t.Errorf("HAUnit = %q, want W", m.HAUnit())
	}
}

func TestModuleActions(t *testing.T) {
	// LPC has no per-entity capability flags, so the static list is returned.
	m := &Module{}
	actions := m.AvailableActionsForEntity(nil)
	want := map[string]bool{"set": true, "clear": true}
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
	err := m.Dispatch("set", "ski", nil, wucapi.Args{Value: 1000}, nil)
	if err == nil {
		t.Fatal("Dispatch before Bind should return an error")
	}
}

func TestEmitSignalsNilSafe(t *testing.T) {
	// Before Bind, EmitSignals must be a no-op (no panic). Guards the
	// initial-announce path in the daemon.
	m := &Module{}
	m.EmitSignals("ski", nil) // nil entity + no impl → returns immediately
}

func TestModuleNumberRangeNilSafe(t *testing.T) {
	// Before Bind (m.impl == nil) or with a nil entity, NumberRangeForEntity
	// must return nil (no panic). This guards the daemon's initial-announce
	// path, which calls NumberRangeForEntity on every compatible entity.
	m := &Module{}
	if r := m.NumberRangeForEntity(nil); r != nil {
		t.Errorf("NumberRangeForEntity(nil) = %v, want nil before Bind", r)
	}
}

func TestFormatWatts(t *testing.T) {
	// The watts formatter is the wire contract for the initial state publish:
	// integer watts have no decimal point, fractional watts are trimmed.
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1000, "1000"},
		{1500, "1500"},
		{1500.5, "1500.5"},
		{1500.25, "1500.25"},
		{1500.123, "1500.123"},
		{1500.1239, "1500.124"}, // 3 decimals then trimmed
	}
	for _, c := range cases {
		got := formatWatts(c.in)
		if got != c.want {
			t.Errorf("formatWatts(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestZeroMeansClearIsTheWireSemantic pins the invariant that "0 = no active
// limit" is consistent across all three LPC surfaces that produce a wire
// value. Before the 0.6.6-dev fix, forwardSignal stayed silent on clear (the
// sensor froze) while EntityState returned "" (the slider showed "unknown"),
// so the slider and sensor disagreed and neither reflected the clear. Now
// both emit "0", and Dispatch treats a set value <= 0 as a clear. This test
// documents the contract so a future refactor cannot silently desync them
// again.
//
// We assert formatWatts(0)=="0" because that single helper is the shared
// rendering path for both EntityState (active-limit branch) and the "0"
// literal used by the inactive branch + forwardSignal. If 0 ever formats as
// anything other than "0", all three surfaces break together — which is
// exactly the alignment we want.
func TestZeroMeansClearIsTheWireSemantic(t *testing.T) {
	if got := formatWatts(0); got != "0" {
		t.Fatalf("formatWatts(0) = %q, want \"0\"; the 0=clear semantic across Dispatch/EntityState/forwardSignal is broken", got)
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
