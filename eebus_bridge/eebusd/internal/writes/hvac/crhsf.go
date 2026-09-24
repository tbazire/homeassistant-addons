// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// crhsf.go — Configuration of Room Heating System Function, exposed as a
// select (or a switch when the room only supports on/off).
//
// The options are the heating operation modes the remote HVAC room supports
// (auto / on / off / eco); choosing one dispatches crhsf.set with the mode as
// the string payload. When the supported set is exactly {on, off} the bridge
// renders the entity as a switch instead of a select (see the component
// mapping in discovery.go).

package hvac

import (
	"fmt"

	"eebusd/internal/writes/wucapi"
	"github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	"github.com/enbility/eebus-go/usecases/ca/crhsf"
	spineapi "github.com/enbility/spine-go/api"
)

const (
	crhsfName      = "crhsf"
	crhsfComponent = "select"
)

// CRHSFModule implements wucapi.WriteUseCase for the CRHSF use case.
type CRHSFModule struct {
	impl    *crhsf.CRHSF
	eventCB wucapi.EventCallback
}

func init() {
	wucapi.Register(&CRHSFModule{})
}

func (m *CRHSFModule) Name() string        { return crhsfName }
func (m *CRHSFModule) HAComponent() string { return crhsfComponent }

// HAUnit returns "" — a select carries no unit of measurement.
func (m *CRHSFModule) HAUnit() string { return "" }

// NumberRangeForEntity returns nil — a select is not a numeric setpoint.
func (m *CRHSFModule) NumberRangeForEntity(spineapi.EntityRemoteInterface) *wucapi.NumberRange {
	return nil
}

// AvailableActionsForEntity lists the heating operation modes the entity
// supports (the HA select options). Until the device announces its modes the
// static fallback list is returned — the write itself fails closed on
// unsupported modes.
func (m *CRHSFModule) AvailableActionsForEntity(entity spineapi.EntityRemoteInterface) []string {
	if m.impl == nil || entity == nil {
		return append([]string(nil), fallbackModes...)
	}
	return sortedModes(m.impl.OperationModes(entity))
}

// Bind wires the underlying eebus-go CRHSF use case.
func (m *CRHSFModule) Bind(localEntity spineapi.EntityLocalInterface, cbs wucapi.Callbacks) {
	if localEntity == nil {
		return
	}
	m.eventCB = cbs.Event
	m.impl = crhsf.NewCRHSF(localEntity, eventRouter(cbs.Event,
		crhsf.UseCaseSupportUpdate,
		crhsf.DataUpdateOperationMode,
	))
}

// UseCase returns the underlying eebus-go use case for svc.AddUseCase.
func (m *CRHSFModule) UseCase() api.UseCaseInterface { return m.impl }

// IsCompatible reports whether the entity advertises CRHSF scenarios.
func (m *CRHSFModule) IsCompatible(entity spineapi.EntityRemoteInterface) bool {
	if m.impl == nil || entity == nil {
		return false
	}
	return len(m.impl.AvailableScenariosForEntity(entity)) > 0
}

// EntityState returns the current heating operation mode ("" when unknown).
func (m *CRHSFModule) EntityState(entity spineapi.EntityRemoteInterface) string {
	if m.impl == nil {
		return ""
	}
	mode, err := m.impl.CurrentOperationMode(entity)
	if err != nil {
		return ""
	}
	return string(mode)
}

// EmitSignals is a no-op — the current mode is forwarded by the mrhsf read
// use case in the scanner package.
func (m *CRHSFModule) EmitSignals(string, spineapi.EntityRemoteInterface) {}

// Dispatch performs the mode write. action is "set" and args.Text carries the
// requested mode; the underlying implementation resolves the mode through the
// room heating relations and fails closed when it is not supported.
func (m *CRHSFModule) Dispatch(action, ski string, entity spineapi.EntityRemoteInterface, args wucapi.Args, resultCB wucapi.ResultCB) error {
	if m.impl == nil {
		return fmt.Errorf("crhsf: not bound to a local entity")
	}
	if !m.IsCompatible(entity) {
		return fmt.Errorf("crhsf: entity not compatible")
	}
	if action != "set" {
		return fmt.Errorf("crhsf: unknown action %q", action)
	}
	mode := ucapi.HvacOperationModeType(args.Text)
	if mode == "" {
		return fmt.Errorf("crhsf: missing mode payload")
	}
	_, err := m.impl.WriteOperationMode(entity, mode, adaptResultCB(resultCB))
	return err
}
