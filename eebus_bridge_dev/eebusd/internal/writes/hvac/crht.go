// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// crht.go — Configuration of Room Heating Temperature, exposed as a number.
//
// The number is the room-air target temperature. The underlying write
// (WriteRoomAirTemperatureSetpoint) is mode-independent and relation-safe: it
// addresses the single room-air setpoint selected by State(), so no mode
// resolution is needed here (unlike cdt). The bridge renders this as the
// climate-style setpoint of the HVACRoom entity.

package hvac

import (
	"fmt"

	"eebusd/internal/writes/wucapi"
	"github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/usecases/ca/crht"
	spineapi "github.com/enbility/spine-go/api"
)

const (
	crhtName      = "crht"
	crhtComponent = "number"
	crhtUnit      = "°C"

	// crhtFallback range (°C) used while the device has not announced its
	// setpoint constraints. Generous on purpose; the device rejects
	// out-of-range values cleanly via SPINE.
	crhtFallbackMin  = 5
	crhtFallbackMax  = 35
	crhtFallbackStep = 0.5
)

// CRHTModule implements wucapi.WriteUseCase for the CRHT use case.
type CRHTModule struct {
	impl    *crht.CRHT
	eventCB wucapi.EventCallback
}

func init() {
	wucapi.Register(&CRHTModule{})
}

func (m *CRHTModule) Name() string        { return crhtName }
func (m *CRHTModule) HAComponent() string { return crhtComponent }
func (m *CRHTModule) HAUnit() string      { return crhtUnit }

// NumberRangeForEntity returns the setpoint constraints from State() (which
// fails closed on incomplete data), or a generous fallback while unknown. A
// range is ALWAYS returned so HA does not apply its default max of 100.
func (m *CRHTModule) NumberRangeForEntity(entity spineapi.EntityRemoteInterface) *wucapi.NumberRange {
	if m.impl != nil && entity != nil {
		if st, err := m.impl.State(entity); err == nil {
			return &wucapi.NumberRange{
				Min:    st.MinValue,
				Max:    st.MaxValue,
				Step:   st.StepSize,
				HasMax: true,
			}
		}
	}
	return &wucapi.NumberRange{
		Min:    crhtFallbackMin,
		Max:    crhtFallbackMax,
		Step:   crhtFallbackStep,
		HasMax: true,
	}
}

// AvailableActionsForEntity lists the action verbs. "set" writes the room
// setpoint (args.Value in °C).
func (m *CRHTModule) AvailableActionsForEntity(spineapi.EntityRemoteInterface) []string {
	return []string{"set"}
}

// Bind wires the underlying eebus-go CRHT use case.
func (m *CRHTModule) Bind(localEntity spineapi.EntityLocalInterface, cbs wucapi.Callbacks) {
	if localEntity == nil {
		return
	}
	m.eventCB = cbs.Event
	m.impl = crht.NewCRHT(localEntity, eventRouter(cbs.Event,
		crht.UseCaseSupportUpdate,
		crht.DataUpdateSetpoints,
		crht.DataUpdateSetpointConstraints,
	))
}

// UseCase returns the underlying eebus-go use case for svc.AddUseCase.
func (m *CRHTModule) UseCase() api.UseCaseInterface { return m.impl }

// IsCompatible reports whether the entity advertises CRHT scenarios.
func (m *CRHTModule) IsCompatible(entity spineapi.EntityRemoteInterface) bool {
	if m.impl == nil || entity == nil {
		return false
	}
	return len(m.impl.AvailableScenariosForEntity(entity)) > 0
}

// EntityState returns the current setpoint value (formatted), "" when unknown.
func (m *CRHTModule) EntityState(entity spineapi.EntityRemoteInterface) string {
	if m.impl == nil {
		return ""
	}
	st, err := m.impl.State(entity)
	if err != nil {
		return ""
	}
	return formatFloat(st.Value)
}

// EmitSignals is a no-op — the room temperature itself is forwarded by the
// mrt read use case in the scanner package.
func (m *CRHTModule) EmitSignals(string, spineapi.EntityRemoteInterface) {}

// Dispatch performs the setpoint write. args.Value is the target temperature
// in °C. The underlying implementation validates changeability, constraints
// and step size, and fails closed on any ambiguity.
func (m *CRHTModule) Dispatch(action, ski string, entity spineapi.EntityRemoteInterface, args wucapi.Args, resultCB wucapi.ResultCB) error {
	if m.impl == nil {
		return fmt.Errorf("crht: not bound to a local entity")
	}
	if !m.IsCompatible(entity) {
		return fmt.Errorf("crht: entity not compatible")
	}
	if action != "set" {
		return fmt.Errorf("crht: unknown action %q", action)
	}
	_, err := m.impl.WriteRoomAirTemperatureSetpoint(entity, args.Value, adaptResultCB(resultCB))
	return err
}
