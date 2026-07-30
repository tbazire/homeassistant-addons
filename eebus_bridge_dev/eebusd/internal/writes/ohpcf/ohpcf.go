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
// the OHPCF scenarios via SPINE. It is exposed in Home Assistant as a single
// climate entity per compatible compressor entity.
//
// This module is generic: it targets any Compressor that announces OHPCF,
// not a specific brand or model. The Saunier Duval VR920 is one concrete
// device where this applies (it advertises SmartEnergyManagementPs + power
// sequences), but any EEBUS-conformant heat pump works the same way.

package ohpcf

import (
	"fmt"
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
	// exposed as. climate gives the user modes (off/auto) and presets
	// (pause/resume) that map naturally onto OHPCF actions.
	haComponent = "climate"
)

// Module is the write-side wrapper for the OHPCF use case. It implements
// wucapi.WriteUseCase and holds the underlying eebus-go *ohpcf.OHPCF once
// Bind has been called.
type Module struct {
	impl    *ohpcf.OHPCF
	eventCB wucapi.EventCallback
}

func init() {
	wucapi.Register(&Module{})
}

// Name returns the use case identifier used in the op prefix.
func (m *Module) Name() string { return useCaseName }

// HAComponent returns the Home Assistant discovery component.
func (m *Module) HAComponent() string { return haComponent }

// HAUnit returns "" — OHPCF is exposed as a climate entity (modes/presets),
// which has no unit of measurement. Implemented to satisfy wucapi.WriteUseCase.
func (m *Module) HAUnit() string { return "" }

// AvailableActions lists the OHPCF actions exposed to the bridge.
func (m *Module) AvailableActions() []string {
	return []string{"schedule", "pause", "resume", "abort"}
}

// Bind wires the underlying eebus-go OHPCF use case to the local entity and
// subscribes to its events. After Bind, UseCase() returns a non-nil value and
// the use case is ready to be added to the service.
func (m *Module) Bind(localEntity spineapi.EntityLocalInterface, eventCB wucapi.EventCallback) {
	if localEntity == nil {
		return
	}
	m.eventCB = eventCB
	cb := api.EntityEventCallback(func(ski string, _ spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
		// Only forward the "support updated" event: this is when the set of
		// remote entities compatible with OHPCF changed (a device just
		// announced support, or revoked it).
		if event != ohpcf.UseCaseSupportUpdate {
			return
		}
		if eventCB != nil {
			eventCB(ski, entity)
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

// EntityState returns the current compressor state as a short, HA-friendly
// string. Empty when the state is not (yet) known.
func (m *Module) EntityState(entity spineapi.EntityRemoteInterface) string {
	if m.impl == nil {
		return ""
	}
	state, err := m.impl.PowerConsumptionProcessState(entity)
	if err != nil {
		return ""
	}
	return mapStateToHA(state)
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

// mapStateToHA converts the SPINE compressor state into a short HA-friendly
// string used for the initial climate action/topic value.
//
// Mapping rationale (HA climate action vocabulary):
//   - running   → heating   (compressor is actively consuming)
//   - paused    → idle      (compressor paused but ready)
//   - available → off       (no process scheduled)
//   - scheduled → off       (planned, not yet running — still reads as off
//     from the action standpoint until it starts)
//   - completed → idle      (process finished, compressor idle)
//   - stopped   → idle      (aborted)
func mapStateToHA(s ucapi.CompressorPowerConsumptionStateType) string {
	switch s {
	case ucapi.CompressorPowerConsumptionStateRunning:
		return "heating"
	case ucapi.CompressorPowerConsumptionStatePaused:
		return "idle"
	case ucapi.CompressorPowerConsumptionStateCompleted, ucapi.CompressorPowerConsumptionStateStopped:
		return "idle"
	case ucapi.CompressorPowerConsumptionStateAvailable, ucapi.CompressorPowerConsumptionStateScheduled:
		return "off"
	default:
		return ""
	}
}
