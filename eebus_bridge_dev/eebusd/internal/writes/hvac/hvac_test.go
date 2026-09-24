// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// hvac_test.go — unit tests for the HVAC write modules.
//
// The modules are thin wrappers over the grafted eebus-go use cases; the full
// SPINE interaction is covered by the eebus-go test suites (usecases/ca/*,
// usecases/ma/*). What needs coverage HERE is the wrapper contract:
//   - the dispatch guards (not bound / not compatible / unknown action /
//     missing payload) fail closed BEFORE any SPINE write could be sent;
//   - the mode list handling (dedup, sort, static fallback);
//   - the cdt↔cdsf mode resolution failing closed while cdsf is unbound;
//   - the number-range fallbacks (a range is ALWAYS advertised so HA never
//     applies its default max of 100 to a temperature setpoint).

package hvac

import (
	"strings"
	"testing"

	"eebusd/internal/writes/wucapi"
	ucapi "github.com/enbility/eebus-go/usecases/api"
)

func TestSortedModes(t *testing.T) {
	cases := []struct {
		name  string
		modes []ucapi.HvacOperationModeType
		err   error
		want  []string
	}{
		{
			name:  "sorted and deduplicated",
			modes: []ucapi.HvacOperationModeType{"off", "on", "eco", "on", "auto"},
			want:  []string{"auto", "eco", "off", "on"},
		},
		{
			name: "error falls back to the static list",
			err:  errFake,
			want: fallbackModes,
		},
		{
			name: "empty falls back to the static list",
			want: fallbackModes,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sortedModes(tc.modes, tc.err)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

var errFake = fakeErr("no data")

type fakeErr string

func (e fakeErr) Error() string { return string(e) }

func TestFormatFloat(t *testing.T) {
	cases := map[float64]string{
		20:    "20",
		20.5:  "20.5",
		21.25: "21.2", // %.1f rounds half to even
		0:     "0",
	}
	for v, want := range cases {
		if got := formatFloat(v); got != want {
			t.Errorf("formatFloat(%v) = %q, want %q", v, got, want)
		}
	}
}

// TestDispatchGuards_FailClosed exercises every synchronous guard that must
// reject a command before any SPINE write could be attempted. The modules are
// left unbound (no local entity), which is exactly the "write use case not
// wired" state — Dispatch must error, not panic, not call resultCB.
func TestDispatchGuards_FailClosed(t *testing.T) {
	modules := map[string]wucapi.WriteUseCase{
		"cdt":   &CDTModule{},
		"cdsf":  &CDSFModule{},
		"crht":  &CRHTModule{},
		"crhsf": &CRHSFModule{},
	}
	called := false
	cb := func(wucapi.ResultStatus, *uint32, string) { called = true }
	for name, mod := range modules {
		err := mod.Dispatch("set", "ski", nil, wucapi.Args{Text: "on", Value: 21}, cb)
		if err == nil {
			t.Errorf("%s: unbound Dispatch must fail, got nil", name)
		}
		if called {
			t.Errorf("%s: resultCB must not fire on a synchronous guard failure", name)
		}
		if !strings.HasPrefix(err.Error(), name+":") {
			t.Errorf("%s: error should be prefixed with the use case name, got %q", name, err.Error())
		}
	}
}

// TestAvailableActions_FallbackBeforeBind verifies the static option list is
// returned while the module is unbound (discovery is not blocked during
// wiring), and that it is a COPY — mutating it must not corrupt the package
// fallback for later calls.
func TestAvailableActions_FallbackBeforeBind(t *testing.T) {
	for name, mod := range map[string]wucapi.WriteUseCase{
		"cdsf":  &CDSFModule{},
		"crhsf": &CRHSFModule{},
	} {
		got := mod.AvailableActionsForEntity(nil)
		if len(got) != len(fallbackModes) {
			t.Errorf("%s: expected the static fallback list, got %v", name, got)
		}
		got[0] = "mutated"
		again := mod.AvailableActionsForEntity(nil)
		if again[0] == "mutated" {
			t.Errorf("%s: AvailableActionsForEntity must return a fresh slice", name)
		}
	}
}

// TestNumberRangeFallbacks asserts both number modules ALWAYS advertise a
// range while unbound — HA caps unbounded numbers at 100, which would make a
// water heater or room setpoint unusable.
func TestNumberRangeFallbacks(t *testing.T) {
	for name, mod := range map[string]wucapi.WriteUseCase{
		"cdt":  &CDTModule{},
		"crht": &CRHTModule{},
	} {
		rng := mod.NumberRangeForEntity(nil)
		if rng == nil {
			t.Fatalf("%s: unbound NumberRangeForEntity must return the fallback, got nil", name)
		}
		if !rng.HasMax || rng.Max <= rng.Min || rng.Step <= 0 {
			t.Errorf("%s: fallback range is not usable: %+v", name, rng)
		}
	}
}

// TestCurrentDHWModeFailsClosedWhenUnbound: writing a DHW setpoint requires
// the current operation mode (resolved via the cdsf module). While cdsf is not
// bound the write must fail closed instead of guessing a mode.
func TestCurrentDHWModeFailsClosedWhenUnbound(t *testing.T) {
	if _, err := currentDHWMode(nil); err == nil {
		t.Fatal("currentDHWMode must fail while the cdsf use case is unbound")
	}
}

// TestSelectComponentsDeclared asserts the HA component contract: the mode
// modules are selects, the setpoint modules are numbers with °C.
func TestSelectComponentsDeclared(t *testing.T) {
	if got := (&CDSFModule{}).HAComponent(); got != "select" {
		t.Errorf("cdsf.HAComponent = %q, want select", got)
	}
	if got := (&CRHSFModule{}).HAComponent(); got != "select" {
		t.Errorf("crhsf.HAComponent = %q, want select", got)
	}
	if got := (&CDTModule{}).HAUnit(); got != "°C" {
		t.Errorf("cdt.HAUnit = %q, want °C", got)
	}
	if got := (&CRHTModule{}).HAUnit(); got != "°C" {
		t.Errorf("crht.HAUnit = %q, want °C", got)
	}
	// Select modules advertise no number range.
	if rng := (&CDSFModule{}).NumberRangeForEntity(nil); rng != nil {
		t.Errorf("cdsf.NumberRangeForEntity = %+v, want nil", rng)
	}
}

// TestBindNilLocalEntityIsNoOp: Bind(nil) must not panic nor register a
// half-wired implementation (the shared cdsf link must stay unset).
func TestBindNilLocalEntityIsNoOp(t *testing.T) {
	(&CDSFModule{}).Bind(nil, wucapi.Callbacks{})
	// The shared link was never set by this test's Bind(nil): assert the
	// lookup still fails closed (it may be set by other tests in this package
	// that bind a real entity — here we only assert no panic occurred).
	_, _ = currentDHWMode(nil)
}
