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

// NumberRange describes the valid input range for a number-like control entity
// (LPC power limit, a future current-limit use case, …). It is surfaced to the
// bridge so the Home Assistant number entity can advertise min/max/step instead
// of falling back to HA's default (max=100), which artificially caps the input.
//
// HasMax distinguishes "the device published a real ceiling" from "no ceiling
// is known": when HasMax is false the bridge MUST omit max entirely (HA then
// treats the number as an unbounded free-form input, and the device itself
// rejects out-of-range values via SPINE). Min and Step are always meaningful
// for a number entity and are published as-is.
//
// A nil *NumberRange (returned by NumberRangeForEntity) means the use case does
// not constrain its input at all — the bridge publishes no min/max/step, which
// is the legacy behavior for use cases that do not implement a range.
type NumberRange struct {
	Min    float64
	Max    float64
	Step   float64
	HasMax bool
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
	// exposed as ("buttons", "number", "switch", "select"). The bridge picks
	// the discovery payload builder from this.
	HAComponent() string

	// HAUnit returns the Home Assistant unit of measurement the exposed entity
	// uses (e.g. "W" for LPC, "A" for a current-limit use case, "°C" for a
	// temperature setpoint). It is only meaningful for number-like components;
	// buttons/switch/select ignore it (return ""). Declared by the use case so
	// the bridge stays agnostic of the physical quantity being controlled.
	HAUnit() string

	// NumberRangeForEntity returns the valid input range for a number-like
	// control entity, or nil when the use case does not constrain its input
	// (buttons/switch/select use cases, or a number use case whose device-side
	// ceiling is not known). The daemon forwards a non-nil range to the bridge
	// so the HA number entity advertises min/max/step; a nil return leaves the
	// number unbounded (legacy behavior). See NumberRange for the HasMax rule.
	NumberRangeForEntity(entity spineapi.EntityRemoteInterface) *NumberRange

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

	// Bind wires the use case to a local entity and the daemon callbacks. Called
	// once at startup, before the use case is added to the service. After Bind,
	// UseCase() returns a non-nil value and IsCompatible/Dispatch/EntityState
	// become usable.
	//
	// cbs carries two callbacks: Event (compatibility/support changes → the
	// daemon emits a "controllable" line) and Signal (per-entity read signals
	// → the daemon emits "uc_signal" lines so the bridge can expose sensors).
	// A use case that has no read signals simply never invokes Signal.
	Bind(localEntity spineapi.EntityLocalInterface, cbs Callbacks)

	// EmitSignals pushes the use case's current read-signal values for one
	// entity (identified by ski) through the Signal callback registered in
	// Bind. The daemon calls it once when a remote entity becomes compatible,
	// so the bridge gets an initial snapshot instead of waiting for the first
	// device notification. Use cases without read signals implement this as a
	// no-op.
	EmitSignals(ski string, entity spineapi.EntityRemoteInterface)
}

// EventCallback is invoked by a bound use case whenever the set of remote
// entities/scenarios it targets changes (typically: a device just announced
// support for this use case). The daemon uses it to emit a "controllable" line.
type EventCallback func(ski string, entity spineapi.EntityRemoteInterface)

// SignalCallback is invoked by a use case to push one read-signal value for a
// remote entity toward the bridge (which exposes it as a sensor). signal is the
// stable identifier ("requested_power", "is_pausable", …), value is the value
// formatted as a string, valueType is one of "number"/"boolean"/"date_time"/
// "duration", and unit is the optional unit ("W", "seconds", …). An empty value
// means the signal is not (yet) available and SHOULD be skipped by the daemon.
type SignalCallback func(ski string, entity spineapi.EntityRemoteInterface, signal, value, valueType, unit string)

// Callbacks bundles the two daemon-side callbacks handed to a use case in Bind.
// Both are optional (nil-safe): a minimal use case may set only Event.
type Callbacks struct {
	Event  EventCallback
	Signal SignalCallback
}

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

// Unregister removes a use case from the registry by name. It is intended for
// test cleanup so a test that registers a fake use case does not leak it into
// later tests (or panic on a duplicate Register when -count > 1). It is a
// no-op if the name is not present. Production code MUST NOT call this: the
// shipped use cases register once at init() and are never removed.
func Unregister(name string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(registry, name)
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
