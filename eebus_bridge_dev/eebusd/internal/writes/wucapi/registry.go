// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package wucapi defines the contract every SPINE WRITE use case module must
// satisfy, plus the registry that lets each module self-register at init() time.
//
// This sub-package has NO dependency on the dispatcher or on any concrete use
// case module: it is the leaf of the writes dependency tree, which lets the
// ohpcf/lpc/... modules import it without creating an import cycle with the
// parent writes package (which imports them back to wire them up).
//
// Design rationale: the bridge is generic (pairs with any EEBUS device). The
// set of relevant write use cases depends entirely on the device, so the
// daemon must not assume any specific one. New use cases are added by writing
// a new sub-package that calls wucapi.Register in its init() and adding one
// blank import line in writes/bind.go — nothing else changes.

package wucapi

import (
	"fmt"
	"sort"
	"sync"

	"github.com/enbility/eebus-go/api"
	spineapi "github.com/enbility/spine-go/api"
)

// Args carries the optional value attached to a command. Only some actions use
// it (e.g. ohpcf.schedule takes a start delay in seconds); others ignore it.
type Args struct {
	// Value is the numeric payload (if any), e.g. a delay in seconds, a power
	// limit in watts, a current in amperes. The use case is responsible for
	// interpreting it.
	Value float64
	// Unit documents what Value means ("seconds", "W", "A", …). The use case
	// may ignore it if the action's unit is fixed.
	Unit string
}

// ResultStatus is the outcome of a dispatched command.
type ResultStatus string

const (
	ResultOK    ResultStatus = "ok"
	ResultError ResultStatus = "error"
)

// ResultCB is invoked by a use case once the SPINE write round-trip completes
// (success or failure). status=ok means the device accepted the command; the
// optional msgCounter is the SPINE message counter for correlation. status=error
// means the device rejected the command or the local stack returned an error;
// errStr carries a short generic reason (never a secret).
type ResultCB func(status ResultStatus, msgCounter *uint32, errStr string)

// WriteUseCase is the contract every write use case module must satisfy.
//
// The methods fall in three groups:
//  1. Metadata: Name / HAComponent / AvailableActionsForEntity — declared up-front so
//     the bridge can build the right HA entity type and advertise actions.
//  2. Compatibility: IsCompatible — asked per remote entity to decide whether
//     this use case applies (typically: the entity advertises matching SPINE
//     scenarios).
//  3. Execution: EntityState (read current state for the initial HA publish)
//     and Dispatch (perform the action).
type WriteUseCase interface {
	// Name is the use case identifier used in the op prefix ("ohpcf", "lpc", …).
	// It MUST be unique across registered use cases and MUST NOT contain a dot.
	Name() string

	// HAComponent is the Home Assistant discovery component this use case is
	// exposed as ("climate", "number", "switch", "select"). The bridge picks
	// the discovery payload builder from this.
	HAComponent() string

	// HAUnit returns the Home Assistant unit of measurement the exposed entity
	// uses (e.g. "W" for LPC, "A" for a current-limit use case, "°C" for a
	// temperature setpoint). It is only meaningful for number-like components;
	// climate/switch/select ignore it (return ""). Declared by the use case so
	// the bridge stays agnostic of the physical quantity being controlled.
	HAUnit() string

	// AvailableActionsForEntity lists the action verbs this use case accepts in
	// Dispatch for the given entity (e.g. ["schedule","pause","resume","abort"]).
	// They are appended to Name to form the op ("ohpcf.pause"). The order is the
	// suggested display order.
	//
	// The list is entity-specific: a use case SHOULD filter out actions the
	// entity does not actually support (e.g. omit "pause" when the device
	// advertises isPausable=false) so the bridge only exposes controls that can
	// succeed. When the per-entity capability is not (yet) known, return the
	// full static list as a safe default.
	AvailableActionsForEntity(entity spineapi.EntityRemoteInterface) []string

	// IsCompatible reports whether a given remote entity can be driven by this
	// use case. Typically delegates to AvailableScenariosForEntity != empty.
	IsCompatible(entity spineapi.EntityRemoteInterface) bool

	// EntityState returns a short, stable, HA-friendly state string describing
	// the current state of the entity for this use case (e.g. "running",
	// "paused", "off"). Used for the initial state publish. Returns "" if the
	// state is not (yet) known.
	EntityState(entity spineapi.EntityRemoteInterface) string

	// Dispatch performs the requested action on the entity. args carries the
	// optional numeric payload; resultCB MUST be invoked exactly once, from any
	// goroutine, when the write completes (sync or async). A synchronous error
	// before the SPINE write is sent must also flow through resultCB (so the
	// caller always gets a single result event).
	Dispatch(action, ski string, entity spineapi.EntityRemoteInterface, args Args, resultCB ResultCB) error

	// UseCase returns the underlying eebus-go use case object, ready to be
	// added to the service via svc.AddUseCase. Returns nil if the module has
	// not been bound to a local entity yet (see Bind).
	UseCase() api.UseCaseInterface

	// Bind wires the use case to a local entity and an event callback. Called
	// once at startup, before the use case is added to the service. After Bind,
	// UseCase() returns a non-nil value and IsCompatible/Dispatch/EntityState
	// become usable.
	Bind(localEntity spineapi.EntityLocalInterface, eventCB EventCallback)
}

// EventCallback is invoked by a bound use case whenever the set of remote
// entities/scenarios it targets changes (typically: a device just announced
// support for this use case). The daemon uses it to emit a "controllable" line.
type EventCallback func(ski string, entity spineapi.EntityRemoteInterface)

// ---- Registry --------------------------------------------------------------

var (
	registryMu sync.RWMutex
	registry   = make(map[string]WriteUseCase)
)

// Register adds a use case module to the global registry. Intended to be called
// from each sub-package's init(). Panics on duplicate names or names containing
// a dot (the dispatcher splits op on "." to route, so the name must not itself
// contain one).
func Register(uc WriteUseCase) {
	name := uc.Name()
	if name == "" {
		panic("writes.Register: empty use case name")
	}
	for _, r := range name {
		if r == '.' {
			panic(fmt.Sprintf("writes.Register: name %q must not contain '.'", name))
		}
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := registry[name]; ok {
		panic(fmt.Sprintf("writes.Register: duplicate use case name %q", name))
	}
	registry[name] = uc
}

// Get returns the use case registered under name, or nil if none.
func Get(name string) WriteUseCase {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[name]
}

// All returns every registered use case, sorted by name for deterministic
// ordering (stable logs, stable HA discovery order).
func All() []WriteUseCase {
	registryMu.RLock()
	out := make([]WriteUseCase, 0, len(registry))
	for _, uc := range registry {
		out = append(out, uc)
	}
	registryMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Names is a convenience returning just the registered use case names.
func Names() []string {
	all := All()
	out := make([]string, len(all))
	for i, uc := range all {
		out[i] = uc.Name()
	}
	return out
}
