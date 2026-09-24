package crht

import (
	"math"

	"github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/features/client"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
)

// State returns the complete room-air temperature setpoint selected for CRHT.
// A setpoint referenced by several operation modes is considered once. The
// method deliberately fails closed when the remote cache is incomplete or
// when several distinct room-air temperature setpoints remain, because the
// client cannot safely choose one of them for a single-zone product model.
func (e *CRHT) State(entity spineapi.EntityRemoteInterface) (ucapi.RoomHeatingSetpointState, error) {
	if !e.IsCompatibleEntityType(entity) {
		return ucapi.RoomHeatingSetpointState{}, api.ErrNoCompatibleEntity
	}

	ids, err := e.roomAirTemperatureSetpointIds(entity)
	if err != nil || len(ids) != 1 {
		return ucapi.RoomHeatingSetpointState{}, api.ErrDataNotAvailable
	}

	sp, err := client.NewSetpoint(e.LocalEntity, entity)
	if err != nil {
		return ucapi.RoomHeatingSetpointState{}, err
	}
	data, err := sp.GetSetpointForId(ids[0])
	if err != nil || data == nil || data.Value == nil {
		return ucapi.RoomHeatingSetpointState{}, api.ErrDataNotAvailable
	}
	constraints, err := sp.GetSetpointConstraintsForId(ids[0])
	if err != nil || constraints == nil || constraints.SetpointRangeMin == nil ||
		constraints.SetpointRangeMax == nil || constraints.SetpointStepSize == nil {
		return ucapi.RoomHeatingSetpointState{}, api.ErrDataNotAvailable
	}

	value := data.Value.GetValue()
	minimum := constraints.SetpointRangeMin.GetValue()
	maximum := constraints.SetpointRangeMax.GetValue()
	step := constraints.SetpointStepSize.GetValue()
	if math.IsNaN(value) || math.IsInf(value, 0) ||
		math.IsNaN(minimum) || math.IsInf(minimum, 0) ||
		math.IsNaN(maximum) || math.IsInf(maximum, 0) ||
		math.IsNaN(step) || math.IsInf(step, 0) || step <= 0 || minimum > maximum {
		return ucapi.RoomHeatingSetpointState{}, api.ErrDataNotAvailable
	}

	return ucapi.RoomHeatingSetpointState{
		Id:           uint(ids[0]),
		Value:        value,
		MinValue:     minimum,
		MaxValue:     maximum,
		StepSize:     step,
		IsActive:     data.IsSetpointActive == nil || *data.IsSetpointActive,
		IsChangeable: data.IsSetpointChangeable == nil || *data.IsSetpointChangeable,
		IsWritable:   sp.IsSetpointListDataWritable(),
	}, nil
}

func (e *CRHT) roomAirTemperatureSetpointIds(
	entity spineapi.EntityRemoteInterface,
) ([]model.SetpointIdType, error) {
	relatedIds, err := e.setpointIds(entity)
	if err != nil {
		return nil, err
	}
	sp, err := client.NewSetpoint(e.LocalEntity, entity)
	if err != nil {
		return nil, err
	}

	seen := make(map[model.SetpointIdType]struct{}, len(relatedIds))
	ids := make([]model.SetpointIdType, 0, len(relatedIds))
	for _, id := range relatedIds {
		if _, exists := seen[id]; exists {
			continue
		}
		description, err := sp.GetSetpointDescriptionForId(id)
		if err != nil || description == nil || description.ScopeType == nil {
			return nil, api.ErrDataNotAvailable
		}
		if *description.ScopeType != model.ScopeTypeTypeRoomAirTemperature {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, api.ErrDataNotAvailable
	}
	return ids, nil
}

// Scenario 1

// return the current room heating temperature setpoints of the HVAC room entity,
// returns ErrDataNotAvailable if no such data is (yet) available
func (e *CRHT) Setpoints(entity spineapi.EntityRemoteInterface) ([]ucapi.Setpoint, error) {
	if !e.IsCompatibleEntityType(entity) {
		return nil, api.ErrNoCompatibleEntity
	}

	setpointIds, err := e.setpointIds(entity)
	if err != nil {
		return nil, err
	}

	sp, err := client.NewSetpoint(e.LocalEntity, entity)
	if err != nil {
		return nil, err
	}

	setpoints := make([]ucapi.Setpoint, 0)
	for _, id := range setpointIds {
		data, err := sp.GetSetpointForId(id)
		if err != nil {
			continue
		}

		setpoint := ucapi.Setpoint{
			Id: uint(id),
			// if absent, the setpoint is active and changeable
			IsActive:     data.IsSetpointActive == nil || *data.IsSetpointActive,
			IsChangeable: data.IsSetpointChangeable == nil || *data.IsSetpointChangeable,
		}
		if data.Value != nil {
			setpoint.Value = data.Value.GetValue()
		}
		if data.ValueMin != nil {
			setpoint.MinValue = data.ValueMin.GetValue()
		}
		if data.ValueMax != nil {
			setpoint.MaxValue = data.ValueMax.GetValue()
		}

		setpoints = append(setpoints, setpoint)
	}

	if len(setpoints) == 0 {
		return nil, api.ErrDataNotAvailable
	}

	return setpoints, nil
}

// return the constraints for the room heating temperature setpoints,
// returns ErrDataNotAvailable if no such data is (yet) available
func (e *CRHT) SetpointConstraints(entity spineapi.EntityRemoteInterface) ([]ucapi.SetpointConstraints, error) {
	if !e.IsCompatibleEntityType(entity) {
		return nil, api.ErrNoCompatibleEntity
	}

	setpointIds, err := e.setpointIds(entity)
	if err != nil {
		return nil, err
	}

	sp, err := client.NewSetpoint(e.LocalEntity, entity)
	if err != nil {
		return nil, err
	}

	constraints := make([]ucapi.SetpointConstraints, 0)
	for _, id := range setpointIds {
		data, err := sp.GetSetpointConstraintsForId(id)
		if err != nil {
			continue
		}

		constraint := ucapi.SetpointConstraints{
			Id: uint(id),
		}
		if data.SetpointRangeMin != nil {
			constraint.MinValue = data.SetpointRangeMin.GetValue()
		}
		if data.SetpointRangeMax != nil {
			constraint.MaxValue = data.SetpointRangeMax.GetValue()
		}
		if data.SetpointStepSize != nil {
			constraint.StepSize = data.SetpointStepSize.GetValue()
		}

		constraints = append(constraints, constraint)
	}

	if len(constraints) == 0 {
		return nil, api.ErrDataNotAvailable
	}

	return constraints, nil
}

// write the room heating temperature setpoint in degree Celsius for a heating
// operation mode (on, off or eco), returns ErrNotSupported if it is not changeable.
//
// The returned message counter and the resultCB let the caller observe the
// device result: a non-zero ResultData.ErrorNumber signals a rejected write.
func (e *CRHT) WriteSetpoint(
	entity spineapi.EntityRemoteInterface,
	mode ucapi.HvacOperationModeType,
	degC float64,
	resultCB func(result model.ResultDataType, msgCounter model.MsgCounterType),
) (*model.MsgCounterType, error) {
	if !e.IsCompatibleEntityType(entity) {
		return nil, api.ErrNoCompatibleEntity
	}

	// the setpoints of the "auto" mode are controlled by a timetable of the device
	if mode == ucapi.HvacOperationModeTypeAuto {
		return nil, api.ErrNotSupported
	}

	setpointId, err := e.setpointIdForMode(entity, mode)
	if err != nil {
		return nil, err
	}

	return e.writeSetpoint(entity, setpointId, degC, resultCB)
}

// WriteRoomAirTemperatureSetpoint writes the single room-air temperature
// setpoint selected by State. Selection is independent of the current HVAC
// operation mode, so callers never have to alias auto or off to another mode.
// The write fails closed if the relation is ambiguous, the cached state is
// incomplete, the setpoint is read-only, or the value violates its range or
// step constraints.
func (e *CRHT) WriteRoomAirTemperatureSetpoint(
	entity spineapi.EntityRemoteInterface,
	degC float64,
	resultCB func(result model.ResultDataType, msgCounter model.MsgCounterType),
) (*model.MsgCounterType, error) {
	state, err := e.State(entity)
	if err != nil {
		return nil, err
	}
	if !state.IsWritable || !state.IsChangeable {
		return nil, api.ErrNotSupported
	}
	if !roomHeatingSetpointValueFitsState(degC, state) {
		return nil, api.ErrDataInvalid
	}

	return e.writeSetpoint(entity, model.SetpointIdType(state.Id), degC, resultCB)
}

func roomHeatingSetpointValueFitsState(value float64, state ucapi.RoomHeatingSetpointState) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) ||
		value < state.MinValue || value > state.MaxValue || state.StepSize <= 0 {
		return false
	}
	steps := math.Round((value - state.MinValue) / state.StepSize)
	return math.Abs(state.MinValue+steps*state.StepSize-value) <= 1e-6
}

func (e *CRHT) writeSetpoint(
	entity spineapi.EntityRemoteInterface,
	setpointID model.SetpointIdType,
	degC float64,
	resultCB func(result model.ResultDataType, msgCounter model.MsgCounterType),
) (*model.MsgCounterType, error) {
	sp, err := client.NewSetpoint(e.LocalEntity, entity)
	if err != nil {
		return nil, err
	}
	if !sp.IsSetpointListDataWritable() {
		return nil, api.ErrNotSupported
	}
	if data, err := sp.GetSetpointForId(setpointID); err == nil &&
		data.IsSetpointChangeable != nil && !*data.IsSetpointChangeable {
		return nil, api.ErrNotSupported
	}

	data := []model.SetpointDataType{{
		SetpointId: util.Ptr(setpointID),
		Value:      model.NewScaledNumberType(degC),
	}}
	msgCounter, err := sp.WriteSetpointListData(data)
	if err != nil {
		return msgCounter, err
	}
	if err := registerSetpointResultCallback(sp, msgCounter, resultCB); err != nil {
		return msgCounter, err
	}
	return msgCounter, nil
}

type setpointResultHandler interface {
	AddResponseCallback(model.MsgCounterType, func(spineapi.ResponseMessage)) error
	RequestSetpoints(
		*model.SetpointListDataSelectorsType,
		*model.SetpointDataElementsType,
	) (*model.MsgCounterType, error)
}

// registerSetpointResultCallback refreshes the setpoint cache after an
// accepted result and before forwarding that result to the caller.
func registerSetpointResultCallback(
	sp setpointResultHandler,
	msgCounter *model.MsgCounterType,
	resultCB func(result model.ResultDataType, msgCounter model.MsgCounterType),
) error {
	if sp == nil || msgCounter == nil {
		return api.ErrDataInvalid
	}

	cb := func(msg spineapi.ResponseMessage) {
		if response, ok := msg.Data.(*model.ResultDataType); ok {
			if response.ErrorNumber == nil || *response.ErrorNumber == model.ErrorNumberTypeNoError {
				_, _ = sp.RequestSetpoints(nil, nil)
			}
			if resultCB != nil {
				resultCB(*response, *msgCounter)
			}
		}
	}
	return sp.AddResponseCallback(*msgCounter, cb)
}

// return the ids of the setpoints related to the heating system function
func (e *CRHT) setpointIds(entity spineapi.EntityRemoteInterface) ([]model.SetpointIdType, error) {
	relations, err := e.setpointRelations(entity)
	if err != nil {
		return nil, err
	}

	var ids []model.SetpointIdType
	for _, relation := range relations {
		ids = append(ids, relation.SetpointId...)
	}

	if len(ids) == 0 {
		return nil, api.ErrDataNotAvailable
	}

	return ids, nil
}

// return the id of the setpoint related to the given heating operation mode
func (e *CRHT) setpointIdForMode(
	entity spineapi.EntityRemoteInterface,
	mode ucapi.HvacOperationModeType,
) (model.SetpointIdType, error) {
	hvac, err := client.NewHvac(e.LocalEntity, entity)
	if err != nil {
		return 0, err
	}

	modeFilter := model.HvacOperationModeDescriptionDataType{
		OperationModeType: util.Ptr(model.HvacOperationModeTypeType(mode)),
	}
	modeDescriptions, err := hvac.GetHvacOperationModeDescriptionsForFilter(modeFilter)
	if err != nil || len(modeDescriptions) == 0 || modeDescriptions[0].OperationModeId == nil {
		return 0, api.ErrDataNotAvailable
	}

	relations, err := e.setpointRelations(entity)
	if err != nil {
		return 0, err
	}

	for _, relation := range relations {
		if relation.OperationModeId != nil &&
			*relation.OperationModeId == *modeDescriptions[0].OperationModeId &&
			len(relation.SetpointId) == 1 {
			return relation.SetpointId[0], nil
		}
	}

	return 0, api.ErrDataNotAvailable
}

// return the setpoint relations of the heating system function
func (e *CRHT) setpointRelations(
	entity spineapi.EntityRemoteInterface,
) ([]model.HvacSystemFunctionSetpointRelationDataType, error) {
	hvac, err := client.NewHvac(e.LocalEntity, entity)
	if err != nil {
		return nil, err
	}

	descFilter := model.HvacSystemFunctionDescriptionDataType{
		SystemFunctionType: util.Ptr(model.HvacSystemFunctionTypeTypeHeating),
	}
	descriptions, err := hvac.GetHvacSystemFunctionDescriptionsForFilter(descFilter)
	// fail closed on an ambiguous result so the wrong system function is never controlled
	if err != nil || len(descriptions) != 1 || descriptions[0].SystemFunctionId == nil {
		return nil, api.ErrDataNotAvailable
	}

	relationFilter := model.HvacSystemFunctionSetpointRelationDataType{
		SystemFunctionId: descriptions[0].SystemFunctionId,
	}

	return hvac.GetHvacSystemFunctionSetpointRelationsForFilter(relationFilter)
}
