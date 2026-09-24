// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// cdt.go — Configuration of DHW Temperature, exposed as a number entity.
//
// The number is the DHW target temperature. EEBUS defines the setpoint per
// operation mode; the daemon resolves the CURRENT mode through the cdsf use
// case (see currentDHWMode) so the user-facing surface stays a single target
// temperature, and fails closed while the mode is unknown. The bridge also
// composes this module into the water_heater entity of the DHWCircuit
// (temperature command/state + min/max/precision).

package hvac

import (
	"fmt"

	"eebusd/internal/writes/wucapi"
	"github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/usecases/ca/cdt"
	spineapi "github.com/enbility/spine-go/api"
)

const (
	cdtName      = "cdt"
	cdtComponent = "number"
	cdtUnit      = "°C"

	// cdtFallback range (°C) used while the device has not announced its
	// setpoint constraints. Generous on purpose: the device's SPINE layer is
	// the final authority and rejects out-of-range values cleanly.
	cdtFallbackMin  = 10
	cdtFallbackMax  = 80
	cdtFallbackStep = 0.5
)

// CDTModule implements wucapi.WriteUseCase for the CDT use case.
type CDTModule struct {
	impl    *cdt.CDT
	eventCB wucapi.EventCallback
}

func init() {
	wucapi.Register(&CDTModule{})
}

func (m *CDTModule) Name() string        { return cdtName }
func (m *CDTModule) HAComponent() string { return cdtComponent }
func (m *CDTModule) HAUnit() string      { return cdtUnit }

// NumberRangeForEntity returns the setpoint constraints advertised by the
// device (min/max/step) for the first declared setpoint, or a generous
// fallback while they are unknown. A range is ALWAYS returned: HA applies its
// own default max (100) to unbounded numbers, which would cap the water
// heater's target temperature nonsensically.
func (m *CDTModule) NumberRangeForEntity(entity spineapi.EntityRemoteInterface) *wucapi.NumberRange {
	if m.impl != nil && entity != nil {
		if cs, err := m.impl.SetpointConstraints(entity); err == nil && len(cs) > 0 {
			return &wucapi.NumberRange{
				Min:    cs[0].MinValue,
				Max:    cs[0].MaxValue,
				Step:   cs[0].StepSize,
				HasMax: true,
			}
		}
	}
	return &wucapi.NumberRange{
		Min:    cdtFallbackMin,
		Max:    cdtFallbackMax,
		Step:   cdtFallbackStep,
		HasMax: true,
	}
}

// AvailableActionsForEntity lists the action verbs. "set" writes the target
// temperature (args.Value in °C).
func (m *CDTModule) AvailableActionsForEntity(spineapi.EntityRemoteInterface) []string {
	return []string{"set"}
}

// Bind wires the underlying eebus-go CDT use case.
func (m *CDTModule) Bind(localEntity spineapi.EntityLocalInterface, cbs wucapi.Callbacks) {
	if localEntity == nil {
		return
	}
	m.eventCB = cbs.Event
	m.impl = cdt.NewCDT(localEntity, eventRouter(cbs.Event,
		cdt.UseCaseSupportUpdate,
		cdt.DataUpdateSetpoints,
		cdt.DataUpdateSetpointConstraints,
	))
}

// UseCase returns the underlying eebus-go use case for svc.AddUseCase.
func (m *CDTModule) UseCase() api.UseCaseInterface { return m.impl }

// IsCompatible reports whether the entity advertises CDT scenarios.
func (m *CDTModule) IsCompatible(entity spineapi.EntityRemoteInterface) bool {
	if m.impl == nil || entity == nil {
		return false
	}
	return len(m.impl.AvailableScenariosForEntity(entity)) > 0
}

// EntityState returns the active setpoint value (formatted), "" when unknown.
func (m *CDTModule) EntityState(entity spineapi.EntityRemoteInterface) string {
	if m.impl == nil {
		return ""
	}
	setpoints, err := m.impl.Setpoints(entity)
	if err != nil {
		return ""
	}
	for _, sp := range setpoints {
		if sp.IsActive {
			return formatFloat(sp.Value)
		}
	}
	if len(setpoints) > 0 {
		return formatFloat(setpoints[0].Value)
	}
	return ""
}

// EmitSignals is a no-op — CDT has no read signals beyond the setpoint state,
// which travels on the controllable refresh path.
func (m *CDTModule) EmitSignals(string, spineapi.EntityRemoteInterface) {}

// Dispatch performs the setpoint write for the CURRENT DHW operation mode.
// args.Value is the target temperature in °C. Fails closed while the current
// mode is unknown: writing a setpoint for a guessed mode could silently
// reconfigure a mode the user did not select.
func (m *CDTModule) Dispatch(action, ski string, entity spineapi.EntityRemoteInterface, args wucapi.Args, resultCB wucapi.ResultCB) error {
	if m.impl == nil {
		return fmt.Errorf("cdt: not bound to a local entity")
	}
	if !m.IsCompatible(entity) {
		return fmt.Errorf("cdt: entity not compatible")
	}
	if action != "set" {
		return fmt.Errorf("cdt: unknown action %q", action)
	}
	mode, err := currentDHWMode(entity)
	if err != nil {
		return err
	}
	_, err = m.impl.WriteSetpoint(entity, mode, args.Value, adaptResultCB(resultCB))
	return err
}
