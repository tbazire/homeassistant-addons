// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package writes: bind.go — late binding of every registered use case to a
// local entity and a shared event callback.
//
// The use case sub-packages register themselves at init() time (so the daemon
// discovers them automatically), but they cannot construct the underlying
// eebus-go use case until they have a local entity. BindAll walks the registry
// and calls Bind on every entry, wiring a shared EventCallback that the daemon
// uses to emit "controllable" lines toward the bridge.
//
// The blank imports below are the single switchboard for "which write use cases
// does this build ship". Adding a new use case later is one import line here,
// plus the new sub-package — nothing in the dispatcher or the registry changes.

package writes

import (
	// Register every shipped write use case. Each line pulls in a sub-package
	// whose init() calls wucapi.Register.
	_ "eebusd/internal/writes/lpc"
	_ "eebusd/internal/writes/ohpcf"

	"eebusd/internal/writes/wucapi"
	spineapi "github.com/enbility/spine-go/api"
)

// BindAll calls Bind(localEntity, cbs) on every registered use case. It is the
// single point the daemon calls at startup to wire the write use cases before
// they are added to the service.
//
// cbs.Event is invoked by each use case whenever a remote entity becomes (or
// stops being) compatible with it; the daemon emits a "controllable" line so
// the bridge can create the matching HA entity. cbs.Signal is invoked on
// per-entity read-signal updates; the daemon emits a "uc_signal" line so the
// bridge can expose the value as a sensor. A use case that has no read signals
// simply never invokes cbs.Signal.
//
// The Signal callback is wrapped per use case so the daemon knows which use
// case a signal belongs to (the upstream callback carries the entity but not
// the use case name).
func BindAll(localEntity spineapi.EntityLocalInterface, cbs wucapi.Callbacks) error {
	if localEntity == nil {
		return errNoLocalEntity
	}
	for _, uc := range wucapi.All() {
		ucName := uc.Name()
		perUC := wucapi.Callbacks{
			Event: cbs.Event,
			Signal: func(ski string, entity spineapi.EntityRemoteInterface, signal, value, valueType, unit string) {
				if cbs.Signal == nil {
					return
				}
				// Tag the signal with its use case so the daemon can emit a
				// fully-qualified uc_signal line. We reuse the signal field to
				// avoid changing the callback signature: "<uc>:<signal>".
				cbs.Signal(ski, entity, ucName+":"+signal, value, valueType, unit)
			},
		}
		uc.Bind(localEntity, perUC)
	}
	return nil
}

// All is a re-export of wucapi.All for the daemon's convenience so it does not
// need to import both packages.
func All() []wucapi.WriteUseCase { return wucapi.All() }

// Names is a re-export of wucapi.Names.
func Names() []string { return wucapi.Names() }

// Get is a re-export of wucapi.Get.
func Get(name string) wucapi.WriteUseCase { return wucapi.Get(name) }

var errNoLocalEntity = wroteError("writes.BindAll: localEntity is nil")

// wroteError is a tiny error type so callers can errors.Is / compare if needed.
type wroteError string

func (e wroteError) Error() string { return string(e) }
