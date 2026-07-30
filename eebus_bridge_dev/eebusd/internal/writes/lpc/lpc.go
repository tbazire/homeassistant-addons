// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package lpc implements the wucapi.WriteUseCase contract for the EEBUS
// "Limitation of Power Consumption" use case (LPC).
//
// LPC lets an Energy Guard (CEM) cap the active power a controllable system
// is allowed to draw. The limit is expressed in watts and applied for an
// optional bounded duration via the LoadControl feature; when the duration
// elapses (or the device enters failsafe) the limit is lifted.
//
// The use case applies to any remote entity that advertises the LPC scenarios
// via SPINE — heat pumps, wallboxes, inverters, batteries, sub-meters… It is
// NOT specific to one brand or model. The bridge exposes it in Home Assistant
// as a single number entity (a power-limit setpoint in W) per compatible
// entity, so the user can drive it like any slider.
//
// Actions:
//   - set    → WriteConsumptionLimit(Value W). A Value <= 0 clears the limit
//     (IsActive=false) per the SPINE semantics of LoadControl.
//   - clear  → WriteConsumptionLimit(IsActive=false): remove the current limit.

package lpc

import (
	"fmt"

	"eebusd/internal/writes/wucapi"
	"github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	"github.com/enbility/eebus-go/usecases/eg/lpc"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

const (
	useCaseName = "lpc"
	// haComponent is the Home Assistant discovery component this use case is
	// exposed as. number gives the user a numeric setpoint (the power limit in
	// watts) which maps naturally onto LPC's consumption limit.
	haComponent = "number"
	// haUnit is the Home Assistant unit of measurement for the limit value.
	// LPC limits active power, which SPINE expresses in watts.
	haUnit = "W"
)

// Module is the write-side wrapper for the LPC use case. It implements
// wucapi.WriteUseCase and holds the underlying eebus-go *lpc.LPC once Bind
// has been called.
type Module struct {
	impl    *lpc.LPC
	eventCB wucapi.EventCallback
}

func init() {
	wucapi.Register(&Module{})
}

// Name returns the use case identifier used in the op prefix.
func (m *Module) Name() string { return useCaseName }

// HAComponent returns the Home Assistant discovery component.
func (m *Module) HAComponent() string { return haComponent }

// HAUnit returns the Home Assistant unit of measurement for the limit value.
func (m *Module) HAUnit() string { return haUnit }

// AvailableActions lists the LPC actions exposed to the bridge.
func (m *Module) AvailableActions() []string {
	return []string{"set", "clear"}
}

// Bind wires the underlying eebus-go LPC use case to the local entity and
// subscribes to its events. After Bind, UseCase() returns a non-nil value and
// the use case is ready to be added to the service.
func (m *Module) Bind(localEntity spineapi.EntityLocalInterface, eventCB wucapi.EventCallback) {
	if localEntity == nil {
		return
	}
	m.eventCB = eventCB
	cb := api.EntityEventCallback(func(ski string, _ spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
		// Only forward the "support updated" event: this is when the set of
		// remote entities compatible with LPC changed (a device just announced
		// support, or revoked it).
		if event != lpc.UseCaseSupportUpdate {
			return
		}
		if eventCB != nil {
			eventCB(ski, entity)
		}
	})
	m.impl = lpc.NewLPC(localEntity, cb)
}

// UseCase returns the underlying eebus-go use case, ready for svc.AddUseCase.
func (m *Module) UseCase() api.UseCaseInterface {
	if m.impl == nil {
		return nil
	}
	return m.impl
}

// IsCompatible reports whether the remote entity advertises LPC scenarios.
func (m *Module) IsCompatible(entity spineapi.EntityRemoteInterface) bool {
	if m.impl == nil || entity == nil {
		return false
	}
	return len(m.impl.AvailableScenariosForEntity(entity)) > 0
}

// EntityState returns the current consumption limit as a string (the watts
// value), or "" when no limit is set / not yet known. This is the initial
// value published on the HA number entity's state topic.
func (m *Module) EntityState(entity spineapi.EntityRemoteInterface) string {
	if m.impl == nil {
		return ""
	}
	limit, err := m.impl.ConsumptionLimit(entity)
	if err != nil || !limit.IsActive {
		// No active limit → empty state. The bridge will publish "" which HA
		// renders as "unknown"; once the user sets a value the device
		// notification refreshes it.
		return ""
	}
	return formatWatts(limit.Value)
}

// Dispatch performs the requested LPC action on the entity. The resultCB is
// invoked exactly once when the SPINE write round-trip completes.
func (m *Module) Dispatch(action, ski string, entity spineapi.EntityRemoteInterface, args wucapi.Args, resultCB wucapi.ResultCB) error {
	if m.impl == nil {
		return fmt.Errorf("lpc: not bound to a local entity")
	}
	if !m.IsCompatible(entity) {
		return fmt.Errorf("lpc: entity not compatible")
	}
	cb := adaptResultCB(resultCB)
	switch action {
	case "set":
		// A power limit in watts. A non-positive value is treated as "clear":
		// SPINE LoadControl limits are absolute magnitudes, so 0 is
		// indistinguishable from "no limit" — we make the intent explicit.
		if args.Value <= 0 {
			limit := ucapi.LoadLimit{IsActive: false}
			_, err := m.impl.WriteConsumptionLimit(entity, limit, cb)
			return err
		}
		// A persistent limit (no bounded duration): the cap stays in effect
		// until the user sets a new value or clears it. Time-bounded limits
		// would require extending the wire contract with a duration field and
		// are intentionally out of scope for this lot.
		limit := ucapi.LoadLimit{
			Value:    args.Value,
			IsActive: true,
		}
		_, err := m.impl.WriteConsumptionLimit(entity, limit, cb)
		return err
	case "clear":
		limit := ucapi.LoadLimit{IsActive: false}
		_, err := m.impl.WriteConsumptionLimit(entity, limit, cb)
		return err
	default:
		return fmt.Errorf("lpc: unknown action %q", action)
	}
}

// adaptResultCB bridges the eebus-go result callback signature
// (func(model.ResultDataType, model.MsgCounterType)) to the wucapi.ResultCB
// signature. The SPINE error number is mapped to ok/error. The shape is
// identical to the ohpcf adapter; it is duplicated here only to keep this
// module self-contained and avoid a cross-use-case dependency.
func adaptResultCB(cb wucapi.ResultCB) func(model.ResultDataType, model.MsgCounterType) {
	if cb == nil {
		return nil
	}
	return func(result model.ResultDataType, msgCounter model.MsgCounterType) {
		errStr := ""
		status := wucapi.ResultOK
		if result.ErrorNumber != nil && *result.ErrorNumber != model.ErrorNumberTypeNoError {
			status = wucapi.ResultError
			errStr = fmt.Sprintf("device rejected (error %d)", uint(*result.ErrorNumber))
			if result.Description != nil && *result.Description != "" {
				errStr = string(*result.Description)
			}
		}
		var mc *uint32
		if msgCounter != 0 {
			v := uint32(msgCounter)
			mc = &v
		}
		cb(status, mc, errStr)
	}
}

// formatWatts renders a power value compactly: integer watts without a decimal
// point, fractional watts with up to 3 significant decimals trimmed. Used for
// the initial HA state publish (subsequent refreshes come from device
// notifications re-rendered by the same rule on the bridge side).
func formatWatts(value float64) string {
	if value == float64(int64(value)) {
		return fmt.Sprintf("%d", int64(value))
	}
	s := fmt.Sprintf("%.3f", value)
	// Trim trailing zeros then a trailing dot, mirroring the bridge formatter.
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}
