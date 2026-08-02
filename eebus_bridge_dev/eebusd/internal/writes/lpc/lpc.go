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
	"time"

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
	impl     *lpc.LPC
	eventCB  wucapi.EventCallback
	signalCB wucapi.SignalCallback
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

// NumberRangeForEntity returns the valid input range for the LPC number entity.
// The ceiling (max) is derived from the device's advertised nominal maximum
// consumption (Scenario 4) when available, so the HA slider reflects the real
// hardware capability instead of HA's default cap of 100 W.
//
// When the device does not expose a nominal max (e.g. the VR920 does not
// advertise the ElectricalConnection characteristic for LPC), nil is returned:
// the bridge then publishes the number without a max, letting the user enter
// any value while the device itself rejects out-of-range limits via SPINE.
// Min is always 0 (a negative active-power limit has no SPINE meaning) and the
// step is 1 W (a sensible finest granularity for a power cap).
func (m *Module) NumberRangeForEntity(entity spineapi.EntityRemoteInterface) *wucapi.NumberRange {
	if m.impl == nil || entity == nil {
		return nil
	}
	max, err := m.impl.ConsumptionNominalMax(entity)
	if err != nil || max <= 0 {
		// No device-side ceiling known → unbounded number (free-form input).
		return nil
	}
	return &wucapi.NumberRange{
		Min:    0,
		Max:    max,
		Step:   1,
		HasMax: true,
	}
}

// AvailableActionsForEntity lists the LPC actions the given entity supports.
// LPC has no per-entity capability flags (any entity advertising the LPC
// scenarios accepts setting and clearing a consumption limit), so the static
// list is returned regardless of the entity.
func (m *Module) AvailableActionsForEntity(spineapi.EntityRemoteInterface) []string {
	return []string{"set", "clear"}
}

// Bind wires the underlying eebus-go LPC use case to the local entity and
// subscribes to its events. After Bind, UseCase() returns a non-nil value and
// the use case is ready to be added to the service.
//
// Two callbacks are wired:
//   - cbs.Event fires on compatibility changes → the daemon emits a
//     "controllable" line (the HA number entity).
//   - cbs.Signal fires on each LPC data-update event → the daemon emits
//     "uc_signal" lines so the bridge exposes the value as a sensor.
func (m *Module) Bind(localEntity spineapi.EntityLocalInterface, cbs wucapi.Callbacks) {
	if localEntity == nil {
		return
	}
	m.eventCB = cbs.Event
	m.signalCB = cbs.Signal
	cb := api.EntityEventCallback(func(ski string, _ spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
		switch event {
		case lpc.UseCaseSupportUpdate:
			if cbs.Event != nil {
				cbs.Event(ski, entity)
			}
		default:
			if cbs.Signal == nil {
				return
			}
			m.forwardSignal(ski, entity, event, cbs.Signal)
		}
	})
	m.impl = lpc.NewLPC(localEntity, cb)
}

// EmitSignals pushes the current LPC read-signal values for one entity through
// the Signal callback registered in Bind. Called by the daemon once when a
// remote entity becomes compatible, so the bridge gets an initial snapshot
// instead of waiting for the first device notification.
func (m *Module) EmitSignals(ski string, entity spineapi.EntityRemoteInterface) {
	if m.impl == nil || entity == nil || m.signalCB == nil {
		return
	}
	for _, event := range lpcDataUpdateEvents {
		m.forwardSignal(ski, entity, event, m.signalCB)
	}
}

// lpcDataUpdateEvents is the set of LPC data-update events we expose as read
// signals. Scenario 1 (the obligation limit) and Scenario 2 (failsafe limit +
// duration) are the user-visible configuration values; Scenario 4 is the
// device's nominal max (its rated power ceiling). Heartbeat (Scenario 3) is
// not surfaced as a sensor.
var lpcDataUpdateEvents = []api.EventType{
	lpc.DataUpdateLimit,
	lpc.DataUpdateFailsafeConsumptionActivePowerLimit,
	lpc.DataUpdateFailsafeDurationMinimum,
	lpc.DataUpdatePowerConsumptionNominalMax,
}

// forwardSignal maps one LPC data-update event to its read signal, re-queries
// the underlying getter, and invokes cb with the typed value. On any error or
// absent value it is a no-op (the signal stays silent until real data arrives).
func (m *Module) forwardSignal(ski string, entity spineapi.EntityRemoteInterface, event api.EventType, cb wucapi.SignalCallback) {
	if m.impl == nil || entity == nil || cb == nil {
		return
	}
	switch event {
	case lpc.DataUpdateLimit:
		limit, err := m.impl.ConsumptionLimit(entity)
		if err != nil {
			return
		}
		// 0 means "no active limit" (consistent with Dispatch, which treats a
		// set value <= 0 as a clear, and with EntityState). We always emit a
		// value here — including 0 when the limit is inactive — so the HA
		// consumption_limit sensor refreshes after a clear instead of staying
		// frozen on the last active value.
		val := limit.Value
		if !limit.IsActive {
			val = 0
		}
		emitNumber(cb, ski, entity, "consumption_limit", val, nil, "W")
	case lpc.DataUpdateFailsafeConsumptionActivePowerLimit:
		v, err := m.impl.FailsafeConsumptionActivePowerLimit(entity)
		emitNumber(cb, ski, entity, "failsafe_power_limit", v, err, "W")
	case lpc.DataUpdateFailsafeDurationMinimum:
		v, err := m.impl.FailsafeDurationMinimum(entity)
		emitDuration(cb, ski, entity, "failsafe_duration_min", v, err)
	case lpc.DataUpdatePowerConsumptionNominalMax:
		v, err := m.impl.ConsumptionNominalMax(entity)
		emitNumber(cb, ski, entity, "nominal_max", v, err, "W")
	}
}

// emit / emitNumber / emitDuration are thin formatters shared with the OHPCF
// module's shape: they skip the signal when the value is not available (err !=
// nil), so a not-yet-known signal simply stays silent rather than publishing a
// meaningless value.
func emit(cb wucapi.SignalCallback, ski string, entity spineapi.EntityRemoteInterface, signal, value, valueType, unit string) {
	if value == "" {
		return
	}
	cb(ski, entity, signal, value, valueType, unit)
}

func emitNumber(cb wucapi.SignalCallback, ski string, entity spineapi.EntityRemoteInterface, signal string, v float64, err error, unit string) {
	if err != nil {
		return
	}
	emit(cb, ski, entity, signal, formatWatts(v), "number", unit)
}

func emitDuration(cb wucapi.SignalCallback, ski string, entity spineapi.EntityRemoteInterface, signal string, d time.Duration, err error) {
	if err != nil {
		return
	}
	emit(cb, ski, entity, signal, fmt.Sprintf("%d", int64(d.Seconds())), "duration", "seconds")
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
// value), or "0" when no limit is active / not yet known. This is the value
// published on the HA number entity's state topic.
//
// "0" (rather than "") is used for an inactive limit so the slider and the
// consumption_limit sensor show the same value after a clear: both read 0,
// which is the wire semantic for "no limit" (see Dispatch). HA renders "0"
// directly on the number entity; "unknown" is reserved for the genuinely
// not-yet-known case, which never reaches here because ConsumptionLimit
// returns the device's reported state (defaulting to inactive).
func (m *Module) EntityState(entity spineapi.EntityRemoteInterface) string {
	if m.impl == nil {
		return ""
	}
	limit, err := m.impl.ConsumptionLimit(entity)
	if err != nil {
		return ""
	}
	if !limit.IsActive {
		return "0"
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
