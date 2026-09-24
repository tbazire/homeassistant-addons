// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// cdsf.go — Configuration of DHW System Function, exposed as a select entity.
//
// The select's options are the DHW operation modes the remote circuit actually
// supports (auto / on / off / eco); choosing one dispatches cdsf.set with the
// mode as the string payload. The bridge also composes this module into the
// water_heater entity of the DHWCircuit (mode command/state).

package hvac

import (
	"fmt"

	"eebusd/internal/writes/wucapi"
	"github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	"github.com/enbility/eebus-go/usecases/ca/cdsf"
	spineapi "github.com/enbility/spine-go/api"
)

const (
	cdsfName      = "cdsf"
	cdsfComponent = "select"
)

// CDSFModule implements wucapi.WriteUseCase for the CDSF use case.
type CDSFModule struct {
	impl    *cdsf.CDSF
	eventCB wucapi.EventCallback
}

func init() {
	wucapi.Register(&CDSFModule{})
}

func (m *CDSFModule) Name() string        { return cdsfName }
func (m *CDSFModule) HAComponent() string { return cdsfComponent }

// HAUnit returns "" — a select carries no unit of measurement.
func (m *CDSFModule) HAUnit() string { return "" }

// NumberRangeForEntity returns nil — a select is not a numeric setpoint.
func (m *CDSFModule) NumberRangeForEntity(spineapi.EntityRemoteInterface) *wucapi.NumberRange {
	return nil
}

// AvailableActionsForEntity lists the DHW operation modes the entity supports.
// These are the HA select options (and the water_heater's mode list). Until
// the device announces its modes the static fallback list is returned — the
// write itself fails closed on unsupported modes.
func (m *CDSFModule) AvailableActionsForEntity(entity spineapi.EntityRemoteInterface) []string {
	if m.impl == nil || entity == nil {
		return append([]string(nil), fallbackModes...)
	}
	return sortedModes(m.impl.OperationModes(entity))
}

// Bind wires the underlying eebus-go CDSF use case. The bound implementation
// is also recorded in the package-level shared state so the cdt module can
// resolve the current operation mode when writing a DHW setpoint.
func (m *CDSFModule) Bind(localEntity spineapi.EntityLocalInterface, cbs wucapi.Callbacks) {
	if localEntity == nil {
		return
	}
	m.eventCB = cbs.Event
	m.impl = cdsf.NewCDSF(localEntity, eventRouter(cbs.Event,
		cdsf.UseCaseSupportUpdate,
		cdsf.DataUpdateOperationMode,
	))
	setSharedCDSF(m.impl)
}

// UseCase returns the underlying eebus-go use case for svc.AddUseCase.
func (m *CDSFModule) UseCase() api.UseCaseInterface { return m.impl }

// IsCompatible reports whether the entity advertises CDSF scenarios.
func (m *CDSFModule) IsCompatible(entity spineapi.EntityRemoteInterface) bool {
	if m.impl == nil || entity == nil {
		return false
	}
	return len(m.impl.AvailableScenariosForEntity(entity)) > 0
}

// EntityState returns the current DHW operation mode ("" when unknown).
func (m *CDSFModule) EntityState(entity spineapi.EntityRemoteInterface) string {
	if m.impl == nil {
		return ""
	}
	mode, err := m.impl.CurrentOperationMode(entity)
	if err != nil {
		return ""
	}
	return string(mode)
}

// EmitSignals is a no-op — the CDSF read values (current mode, overrun state)
// are forwarded by the mdsf read use case in the scanner package.
func (m *CDSFModule) EmitSignals(string, spineapi.EntityRemoteInterface) {}

// Dispatch performs the mode write. action is "set" and args.Text carries the
// requested mode; the underlying implementation resolves the mode through the
// DHW system-function relations and fails closed when it is not supported.
func (m *CDSFModule) Dispatch(action, ski string, entity spineapi.EntityRemoteInterface, args wucapi.Args, resultCB wucapi.ResultCB) error {
	if m.impl == nil {
		return fmt.Errorf("cdsf: not bound to a local entity")
	}
	if !m.IsCompatible(entity) {
		return fmt.Errorf("cdsf: entity not compatible")
	}
	if action != "set" {
		return fmt.Errorf("cdsf: unknown action %q", action)
	}
	mode := ucapi.HvacOperationModeType(args.Text)
	if mode == "" {
		return fmt.Errorf("cdsf: missing mode payload")
	}
	_, err := m.impl.WriteOperationMode(entity, mode, adaptResultCB(resultCB))
	return err
}
