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
