// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package ohpcf implements the wucapi.WriteUseCase contract for the EEBUS
// "Optimization of Self-Consumption by Heat-Pump Compressor Flexibility"
// use case (OHPCF).
//
// OHPCF lets a CEM schedule, pause, resume or abort the optional power
// consumption process of a compressor (typically a heat-pump compressor that
// can run flexibly to optimize self-consumption of local PV production).
//
// The use case applies to remote entities of type Compressor that advertise
// the OHPCF scenarios via SPINE. It is exposed in Home Assistant as one
// "buttons" component per compatible compressor entity — the bridge expands
// that into a HA button per action (schedule/pause/resume/abort), each a
// momentary trigger. The compressor's runtime state is exposed separately as
// a read-only process_state sensor (see forwardSignal).
//
// This module is generic: it targets any Compressor that announces OHPCF,
// not a specific brand or model. The Saunier Duval VR920 is one concrete
// device where this applies (it advertises SmartEnergyManagementPs + power
// sequences), but any EEBUS-conformant heat pump works the same way.

package ohpcf

import (
	"fmt"
	"strings"
	"time"

	"eebusd/internal/writes/wucapi"
	"github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	"github.com/enbility/eebus-go/usecases/cem/ohpcf"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

const (
	useCaseName = "ohpcf"
	// haComponent is the Home Assistant discovery component this use case is
	// exposed as. "buttons" tells the bridge to generate one HA button entity
	// per action (schedule/pause/resume/abort). Buttons are momentary triggers
	// with no state, which matches OHPCF semantics better than a climate
	// entity (modes/presets) did: each OHPCF action is a fire-and-forget
	// command, not a mode to dwell in.
	haComponent = "buttons"
)

// Module is the write-side wrapper for the OHPCF use case. It implements
// wucapi.WriteUseCase and holds the underlying eebus-go *ohpcf.OHPCF once
// Bind has been called.
type Module struct {
	impl     *ohpcf.OHPCF
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

// HAUnit returns "" — OHPCF is exposed as buttons (momentary triggers), which
// have no unit of measurement. Implemented to satisfy wucapi.WriteUseCase.
func (m *Module) HAUnit() string { return "" }

// NumberRangeForEntity returns nil — OHPCF buttons are not a numeric setpoint,
// so they have no min/max/step range. Implemented to satisfy wucapi.WriteUseCase.
func (m *Module) NumberRangeForEntity(spineapi.EntityRemoteInterface) *wucapi.NumberRange {
	return nil
}

// AvailableActionsForEntity lists the OHPCF actions the given entity actually
// supports. "schedule" is always offered (it is the core scheduling verb and
// the device has already advertised the OHPCF scenarios to reach this point).
// "pause"/"resume" are offered only when the device advertises isPausable
// (OHPCF-011/6): a non-pausable compressor cannot honour them, so exposing a
// control that would always be rejected is worse than not exposing it.
// "abort" is offered only when the device advertises isStoppable (OHPCF-011/5).
//
// When the capability queries fail (data not yet available), we fall back to
// the full static list so the entity is still usable while the device is still
// announcing its details — the dispatcher will surface a clean command_result
// if a control turns out unsupported at run time.
func (m *Module) AvailableActionsForEntity(entity spineapi.EntityRemoteInterface) []string {
	// No underlying use case yet (Bind not called) or no entity: return the
	// full static list so discovery is not blocked while wiring completes.
	if m.impl == nil || entity == nil {
		return []string{"schedule", "pause", "resume", "abort"}
	}

	actions := []string{"schedule"}

	pausable, errPause := m.impl.ConsumptionIsPausable(entity)
	if errPause != nil || pausable {
		actions = append(actions, "pause", "resume")
	}
	stoppable, errStop := m.impl.ConsumptionIsStoppable(entity)
	if errStop != nil || stoppable {
		actions = append(actions, "abort")
	}
	return actions
}

// Bind wires the underlying eebus-go OHPCF use case to the local entity and
// subscribes to its events. After Bind, UseCase() returns a non-nil value and
// the use case is ready to be added to the service.
//
// Two callbacks are wired:
//   - cbs.Event fires ONLY on compatibility changes (a device announced/revoked
//     OHPCF support) → the daemon emits a "controllable" line so the bridge
//     (re)creates the buttons.
//   - cbs.Signal fires on every OHPCF data-update event, including the
//     compressor runtime state (DataUpdateConsumptionState) → the daemon emits
//     a "uc_signal" line so the bridge exposes/refreshes the matching sensor.
//
// DataUpdateConsumptionState is routed to cbs.Signal (not cbs.Event): the
// compressor state is now surfaced as a read-only process_state sensor (a
// faithful reflection of the device's state) rather than as the action of a
// climate entity. Buttons carry no state, so there is nothing to refresh on
// the buttons themselves — the sensor is the single source of truth for the
// process state.
func (m *Module) Bind(localEntity spineapi.EntityLocalInterface, cbs wucapi.Callbacks) {
	if localEntity == nil {
		return
	}
	m.eventCB = cbs.Event
	m.signalCB = cbs.Signal
	cb := api.EntityEventCallback(func(ski string, _ spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
		switch event {
		case ohpcf.UseCaseSupportUpdate:
			// A device just announced (or revoked) OHPCF support. Always
			// re-emit so discovery (or removal) is reflected in HA.
			if cbs.Event != nil {
				cbs.Event(ski, entity)
			}
		default:
			// Any OHPCF data-update event (including DataUpdateConsumptionState,
			// which yields the process_state sensor): re-query the matching
			// getter and forward the current value as a uc_signal.
			if cbs.Signal == nil {
				return
			}
			m.forwardSignal(ski, entity, event, cbs.Signal)
		}
	})
	m.impl = ohpcf.NewOHPCF(localEntity, cb)
}

// UseCase returns the underlying eebus-go use case, ready for svc.AddUseCase.
func (m *Module) UseCase() api.UseCaseInterface {
	if m.impl == nil {
		return nil
	}
	return m.impl
}

// IsCompatible reports whether the remote entity advertises OHPCF scenarios.
func (m *Module) IsCompatible(entity spineapi.EntityRemoteInterface) bool {
	if m.impl == nil || entity == nil {
		return false
	}
	return len(m.impl.AvailableScenariosForEntity(entity)) > 0
}

// EntityState returns the current compressor state as a short, lowercase
// string (the raw SPINE enum name: "running"/"paused"/…). Empty when the
// state is not (yet) known. This feeds c.State in the controllable line; for
// the buttons component it is not surfaced on an entity (buttons carry no
// state), but the same value is also pushed as the process_state sensor via
// the Signal path, which is where the user reads it.
func (m *Module) EntityState(entity spineapi.EntityRemoteInterface) string {
	if m.impl == nil {
		return ""
	}
	state, err := m.impl.PowerConsumptionProcessState(entity)
	if err != nil {
		return ""
	}
	return mapStateToRaw(state)
}

// EmitSignals pushes all OHPCF read-signal values for one entity through the
// Signal callback registered in Bind. Called by the daemon once when a remote
// entity becomes compatible, so the bridge gets an initial snapshot instead of
// waiting for the first device notification (HA would otherwise show "unknown").
// Each getter is queried independently; if a value is not (yet) available the
// signal is skipped.
func (m *Module) EmitSignals(ski string, entity spineapi.EntityRemoteInterface) {
	if m.impl == nil || entity == nil || m.signalCB == nil {
		return
	}
	for _, event := range ohpcfDataUpdateEvents {
		m.forwardSignal(ski, entity, event, m.signalCB)
	}
}

// ohpcfDataUpdateEvents is the full set of OHPCF Scenario-1 data-update events,
// including the compressor runtime state (DataUpdateConsumptionState). Used by
// EmitSignals to push an initial snapshot in a stable order, so the
// process_state sensor gets a value immediately rather than staying "unknown"
// until the first device NOTIFY.
var ohpcfDataUpdateEvents = []api.EventType{
	ohpcf.DataUpdateConsumptionState,
	ohpcf.DataUpdateRequestedPowerEstimate,
	ohpcf.DataUpdateRequestedPowerMax,
	ohpcf.DataUpdateConsumptionStartTime,
	ohpcf.DataUpdateConsumptionIsPausable,
	ohpcf.DataUpdateConsumptionIsStoppable,
	ohpcf.DataUpdateMinimalRunDuration,
	ohpcf.DataUpdateMinimalPauseDuration,
}

// forwardSignal maps one OHPCF data-update event to its read signal, re-queries
// the underlying getter, and invokes cb with the typed value. On any error or
// absent value it is a no-op (the signal stays silent until real data arrives).
//
// The ski is best-effort here: the upstream event callback does not carry it
// per-signal, so EmitSignals passes "". The real ski is attached by the daemon
// when it already knows the entity's device. (In practice the per-event path
// fires from the eebus-go callback which DOES carry ski; EmitSignals is the
// only caller passing "" and the daemon re-derives ski from the entity.)
func (m *Module) forwardSignal(ski string, entity spineapi.EntityRemoteInterface, event api.EventType, cb wucapi.SignalCallback) {
	if m.impl == nil || entity == nil || cb == nil {
		return
	}
	switch event {
	case ohpcf.DataUpdateRequestedPowerEstimate:
		v, err := m.impl.RequestedPowerEstimate(entity)
		emitNumber(cb, ski, entity, "requested_power", v, err, "W")
	case ohpcf.DataUpdateRequestedPowerMax:
		v, err := m.impl.RequestedPowerMax(entity)
		emitNumber(cb, ski, entity, "max_power", v, err, "W")
	case ohpcf.DataUpdateConsumptionStartTime:
		v, err := m.impl.PowerConsumptionProcessStartTime(entity)
		if err == nil {
			emit(cb, ski, entity, "start_time", v.UTC().Format(time.RFC3339), "date_time", "")
		}
	case ohpcf.DataUpdateConsumptionIsPausable:
		v, err := m.impl.ConsumptionIsPausable(entity)
		emitBool(cb, ski, entity, "is_pausable", v, err)
	case ohpcf.DataUpdateConsumptionIsStoppable:
		v, err := m.impl.ConsumptionIsStoppable(entity)
		emitBool(cb, ski, entity, "is_stoppable", v, err)
	case ohpcf.DataUpdateMinimalRunDuration:
		v, err := m.impl.PowerConsumptionMinimalRunDuration(entity)
		emitDuration(cb, ski, entity, "min_run_duration", v, err)
	case ohpcf.DataUpdateMinimalPauseDuration:
		v, err := m.impl.PowerConsumptionMinimalPauseDuration(entity)
		emitDuration(cb, ski, entity, "min_pause_duration", v, err)
	case ohpcf.DataUpdateConsumptionState:
		// The compressor's runtime state, surfaced as a read-only
		// process_state sensor (value type "string"). The value is the raw
		// SPINE enum name (running/paused/scheduled/available/completed/
		// stopped), which is a faithful reflection of the device state —
		// unlike the old climate "action" vocabulary that conflated several
		// distinct states into heating/idle/off.
		state, err := m.impl.PowerConsumptionProcessState(entity)
		if err == nil {
			emit(cb, ski, entity, "process_state", mapStateToRaw(state), "string", "")
		}
	}
}

// emit / emitNumber / emitBool / emitDuration are thin formatters that skip the
// signal when the value is not available (err != nil). An empty value is the
// daemon's cue to skip the line entirely.
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
	emit(cb, ski, entity, signal, formatFloat(v), "number", unit)
}

func emitBool(cb wucapi.SignalCallback, ski string, entity spineapi.EntityRemoteInterface, signal string, v bool, err error) {
	if err != nil {
		return
	}
	val := "false"
	if v {
		val = "true"
	}
	emit(cb, ski, entity, signal, val, "boolean", "")
}

func emitDuration(cb wucapi.SignalCallback, ski string, entity spineapi.EntityRemoteInterface, signal string, d time.Duration, err error) {
	if err != nil {
		return
	}
	// Whole seconds; fractional seconds are rounded to keep the wire value clean.
	emit(cb, ski, entity, signal, fmt.Sprintf("%d", int64(d.Seconds())), "duration", "seconds")
}

// formatFloat renders a numeric value compactly (integer without decimals,
// fractional with up to 3 significant decimals trimmed), mirroring the bridge
// formatter so the sensor state looks consistent.
func formatFloat(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	s := fmt.Sprintf("%.3f", v)
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}

// Dispatch performs the requested OHPCF action on the entity. The resultCB is
// invoked exactly once when the SPINE write round-trip completes.
func (m *Module) Dispatch(action, ski string, entity spineapi.EntityRemoteInterface, args wucapi.Args, resultCB wucapi.ResultCB) error {
	if m.impl == nil {
		return fmt.Errorf("ohpcf: not bound to a local entity")
	}
	if !m.IsCompatible(entity) {
		return fmt.Errorf("ohpcf: entity not compatible")
	}
	cb := adaptResultCB(resultCB)
	switch action {
	case "schedule":
		startIn := time.Duration(args.Value) * time.Second
		if args.Value < 0 || args.Unit != "" && args.Unit != "seconds" {
			// Non-fatal: clamp to immediate start rather than refusing.
			startIn = 0
		}
		_, err := m.impl.SchedulePowerConsumptionProcess(entity, startIn, cb)
		return err
	case "pause":
		_, err := m.impl.PausePowerConsumptionProcess(entity, cb)
		return err
	case "resume":
		_, err := m.impl.ResumePowerConsumptionProcess(entity, cb)
		return err
	case "abort":
		_, err := m.impl.AbortPowerConsumptionProcess(entity, cb)
		return err
	default:
		return fmt.Errorf("ohpcf: unknown action %q", action)
	}
}

// adaptResultCB bridges the eebus-go result callback signature
// (func(model.ResultDataType, model.MsgCounterType)) to the wucapi.ResultCB
// signature. The SPINE error number is mapped to ok/error.
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

// mapStateToRaw converts the SPINE compressor state enum into its lowercase
// string name, used as the process_state sensor value. The enum values are
// already clean lowercase strings ("running", "paused", "scheduled",
// "available", "completed", "stopped"), so we just pass them through. This is
// a faithful reflection of the device state, unlike the former climate
// "action" mapping (heating/idle/off) which conflated several distinct
// states and is what motivated the move from climate to buttons + sensor.
func mapStateToRaw(s ucapi.CompressorPowerConsumptionStateType) string {
	return strings.ToLower(string(s))
}
