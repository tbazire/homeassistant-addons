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

// BindAll calls Bind(localEntity, eventCB) on every registered use case. It is
// the single point the daemon calls at startup to wire the write use cases
// before they are added to the service.
//
// eventCB is invoked by each use case whenever a remote entity becomes (or
// stops being) compatible with it; the daemon uses it to emit a "controllable"
// line so the bridge can create the matching HA entity.
func BindAll(localEntity spineapi.EntityLocalInterface, eventCB wucapi.EventCallback) error {
	if localEntity == nil {
		return errNoLocalEntity
	}
	for _, uc := range wucapi.All() {
		uc.Bind(localEntity, eventCB)
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
