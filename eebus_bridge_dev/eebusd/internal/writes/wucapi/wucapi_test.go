// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire

package wucapi

import (
	"testing"

	"github.com/enbility/eebus-go/api"
	spineapi "github.com/enbility/spine-go/api"
)

// fakeUseCase is a minimal WriteUseCase implementation for registry testing.
// It does not touch SPINE: it only records what the dispatcher asked.
type fakeUseCase struct {
	name     string
	comp     string
	actions  []string
	bound    bool
	compat   bool
	state    string
	dispAct  string // last Dispatch action received
	dispErr  error  // error returned by Dispatch
	dispArgs Args
	gotCB    bool
}

func (f *fakeUseCase) Name() string        { return f.name }
func (f *fakeUseCase) HAComponent() string { return f.comp }
func (f *fakeUseCase) HAUnit() string      { return "" }
func (f *fakeUseCase) AvailableActionsForEntity(spineapi.EntityRemoteInterface) []string {
	return f.actions
}
func (f *fakeUseCase) IsCompatible(spineapi.EntityRemoteInterface) bool  { return f.compat }
func (f *fakeUseCase) EntityState(spineapi.EntityRemoteInterface) string { return f.state }
func (f *fakeUseCase) UseCase() api.UseCaseInterface                     { return nil }
func (f *fakeUseCase) Dispatch(action, ski string, entity spineapi.EntityRemoteInterface, args Args, cb ResultCB) error {
	f.dispAct = action
	f.dispArgs = args
	if f.dispErr != nil {
		return f.dispErr
	}
	if cb != nil {
		f.gotCB = true
		cb(ResultOK, nil, "")
	}
	return nil
}
func (f *fakeUseCase) Bind(spineapi.EntityLocalInterface, EventCallback) { f.bound = true }

func TestRegisterAndGet(t *testing.T) {
	// Use a sub-test with a freshly registered fake. We cannot easily unload
	// the real ohpcf registered by init() in the parent package, so we pick a
	// name that does not collide and only assert Get returns it.
	defer func() {
		// Clean the registry so this test is repeatable across runs / packages.
		registryMu.Lock()
		delete(registry, "fakeuc_test")
		registryMu.Unlock()
	}()
	f := &fakeUseCase{name: "fakeuc_test", comp: "number", actions: []string{"set"}}
	Register(f)

	got := Get("fakeuc_test")
	if got == nil {
		t.Fatal("Get returned nil after Register")
	}
	if got.Name() != "fakeuc_test" {
		t.Errorf("Get returned wrong name: %q", got.Name())
	}
}

func TestRegisterRejectsBadNames(t *testing.T) {
	cases := []struct {
		name string
		uc   WriteUseCase
	}{
		{"empty", &fakeUseCase{name: ""}},
		{"dot", &fakeUseCase{name: "a.b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("Register(%q) did not panic", c.uc.Name())
				}
				// Clean any half-registered entry just in case.
				registryMu.Lock()
				delete(registry, c.uc.Name())
				registryMu.Unlock()
			}()
			Register(c.uc)
		})
	}
}

func TestAllSortedByName(t *testing.T) {
	// Register two fake use cases and assert All()/Names() return them sorted.
	// (The real ohpcf module is registered from the parent writes package's
	// init, which does not run when this package's test binary is built in
	// isolation — so we register our own to exercise the sorting.)
	defer func() {
		registryMu.Lock()
		delete(registry, "zz_sort_a")
		delete(registry, "aa_sort_b")
		registryMu.Unlock()
	}()
	Register(&fakeUseCase{name: "zz_sort_a"})
	Register(&fakeUseCase{name: "aa_sort_b"})

	names := Names()
	if len(names) < 2 {
		t.Fatalf("Names() = %v, want at least 2 entries", names)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("Names() not sorted: %v", names)
		}
	}
}

func TestArgsAndStatusConstants(t *testing.T) {
	// Smoke-check the wire contract strings are stable (they ARE the contract
	// with the bridge). Changing them silently would break the integration.
	if ResultOK != "ok" {
		t.Errorf("ResultOK = %q, want %q", ResultOK, "ok")
	}
	if ResultError != "error" {
		t.Errorf("ResultError = %q, want %q", ResultError, "error")
	}
}
