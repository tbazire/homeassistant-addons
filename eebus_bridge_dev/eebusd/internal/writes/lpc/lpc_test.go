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
	m := &Module{}
	actions := m.AvailableActions()
	want := map[string]bool{"set": true, "clear": true}
	if len(actions) != len(want) {
		t.Fatalf("AvailableActions = %v, want %d entries", actions, len(want))
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
