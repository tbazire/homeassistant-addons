package cdsf

import (
	"github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/features/client"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
)

// WriteCapabilities returns the currently usable CDSF writes for the DHW
// circuit. Missing or ambiguous cached metadata fails closed so callers never
// advertise a write that WriteOperationMode, StartOneTimeDhw or StopOneTimeDhw
// would predictably reject.
func (e *CDSF) WriteCapabilities(
	entity spineapi.EntityRemoteInterface,
) (ucapi.DHWSystemFunctionWriteCapabilities, error) {
	if !e.IsCompatibleEntityType(entity) {
		return ucapi.DHWSystemFunctionWriteCapabilities{}, api.ErrNoCompatibleEntity
	}

	hvac, err := client.NewHvac(e.LocalEntity, entity)
	if err != nil {
		return ucapi.DHWSystemFunctionWriteCapabilities{}, err
	}

	capabilities := ucapi.DHWSystemFunctionWriteCapabilities{}
	if e.IsScenarioAvailableAtEntity(entity, 1) {
		systemFunctionId, idErr := e.systemFunctionId(entity)
		systemFunction, dataErr := hvac.GetHvacSystemFunctionForId(systemFunctionId)
		modes, modesErr := e.OperationModes(entity)
		capabilities.OperationMode = idErr == nil && dataErr == nil && systemFunction != nil &&
			modesErr == nil && len(modes) > 0 &&
			hvac.IsHvacSystemFunctionListDataWritable() &&
			(systemFunction.IsOperationModeIdChangeable == nil || *systemFunction.IsOperationModeIdChangeable)
	}

	startAvailable := e.IsScenarioAvailableAtEntity(entity, 2)
	stopAvailable := e.IsScenarioAvailableAtEntity(entity, 3)
	if startAvailable || stopAvailable {
		overrunId, idErr := e.overrunId(entity)
		overrun, dataErr := hvac.GetHvacOverrunForId(overrunId)
		overrunWritable := idErr == nil && dataErr == nil && overrun != nil && hvac.IsHvacOverrunListDataWritable() &&
			(overrun.IsOverrunStatusChangeable == nil || *overrun.IsOverrunStatusChangeable)
		capabilities.StartOneTimeDhw = startAvailable && overrunWritable
		capabilities.StopOneTimeDhw = stopAvailable && overrunWritable
	}

	return capabilities, nil
}

// Scenario 1

// return the DHW operation modes supported by the DHW circuit,
// returns ErrDataNotAvailable if no such data is (yet) available
func (e *CDSF) OperationModes(entity spineapi.EntityRemoteInterface) ([]ucapi.HvacOperationModeType, error) {
	if !e.IsCompatibleEntityType(entity) {
		return nil, api.ErrNoCompatibleEntity
	}

	hvac, err := client.NewHvac(e.LocalEntity, entity)
	if err != nil {
		return nil, err
	}

	systemFunctionId, err := e.systemFunctionId(entity)
	if err != nil {
		return nil, err
	}

	relationFilter := model.HvacSystemFunctionOperationModeRelationDataType{
		SystemFunctionId: &systemFunctionId,
	}
	relations, err := hvac.GetHvacSystemFunctionOperationModeRelationsForFilter(relationFilter)
	if err != nil || len(relations) == 0 {
		return nil, api.ErrDataNotAvailable
	}

	modes := make([]ucapi.HvacOperationModeType, 0)
	for _, relation := range relations {
		for _, modeId := range relation.OperationModeId {
			description, err := hvac.GetHvacOperationModeDescriptionForId(modeId)
			if err != nil || description.OperationModeType == nil {
				continue
			}

			modes = append(modes, ucapi.HvacOperationModeType(*description.OperationModeType))
		}
	}

	if len(modes) == 0 {
		return nil, api.ErrDataNotAvailable
	}

	return modes, nil
}

// return the current DHW operation mode of the DHW circuit,
// returns ErrDataNotAvailable if no such data is (yet) available
func (e *CDSF) CurrentOperationMode(entity spineapi.EntityRemoteInterface) (ucapi.HvacOperationModeType, error) {
	if !e.IsCompatibleEntityType(entity) {
		return "", api.ErrNoCompatibleEntity
	}

	hvac, err := client.NewHvac(e.LocalEntity, entity)
	if err != nil {
		return "", err
	}

	systemFunctionId, err := e.systemFunctionId(entity)
	if err != nil {
		return "", err
	}

	data, err := hvac.GetHvacSystemFunctionForId(systemFunctionId)
	if err != nil || data.CurrentOperationModeId == nil {
		return "", api.ErrDataNotAvailable
	}

	description, err := hvac.GetHvacOperationModeDescriptionForId(*data.CurrentOperationModeId)
	if err != nil || description.OperationModeType == nil {
		return "", api.ErrDataNotAvailable
	}

	return ucapi.HvacOperationModeType(*description.OperationModeType), nil
}

// set the DHW operation mode of the DHW circuit,
// returns ErrNotSupported if the operation mode is not changeable or not supported.
//
// The returned message counter and the resultCB let the caller observe the
// device result: a non-zero ResultData.ErrorNumber signals a rejected write.
func (e *CDSF) WriteOperationMode(
	entity spineapi.EntityRemoteInterface,
	mode ucapi.HvacOperationModeType,
	resultCB func(result model.ResultDataType, msgCounter model.MsgCounterType),
) (*model.MsgCounterType, error) {
	if !e.IsCompatibleEntityType(entity) {
		return nil, api.ErrNoCompatibleEntity
	}

	hvac, err := client.NewHvac(e.LocalEntity, entity)
	if err != nil {
		return nil, err
	}

	systemFunctionId, err := e.systemFunctionId(entity)
	if err != nil {
		return nil, err
	}

	data, err := hvac.GetHvacSystemFunctionForId(systemFunctionId)
	if err != nil {
		return nil, api.ErrDataNotAvailable
	}

	// only an explicit false blocks the write; an omitted flag is tolerated, as
	// some devices accept the write without advertising the changeability flag
	if data.IsOperationModeIdChangeable != nil && !*data.IsOperationModeIdChangeable {
		return nil, api.ErrNotSupported
	}

	// resolve the requested mode through the DHW system-function relation, so a
	// mode that exists globally but is not related to this function is rejected
	relationFilter := model.HvacSystemFunctionOperationModeRelationDataType{
		SystemFunctionId: &systemFunctionId,
	}
	relations, err := hvac.GetHvacSystemFunctionOperationModeRelationsForFilter(relationFilter)
	if err != nil || len(relations) == 0 {
		return nil, api.ErrDataNotAvailable
	}

	modeIds := make(map[model.HvacOperationModeIdType]struct{})
	for _, relation := range relations {
		for _, id := range relation.OperationModeId {
			description, err := hvac.GetHvacOperationModeDescriptionForId(id)
			if err != nil || description.OperationModeType == nil {
				continue
			}
			if ucapi.HvacOperationModeType(*description.OperationModeType) == mode {
				modeIds[id] = struct{}{}
			}
		}
	}
	if len(modeIds) == 0 {
		return nil, api.ErrNotSupported
	}
	if len(modeIds) != 1 {
		return nil, api.ErrDataNotAvailable
	}
	var modeId model.HvacOperationModeIdType
	for id := range modeIds {
		modeId = id
	}

	writeData := []model.HvacSystemFunctionDataType{
		{
			SystemFunctionId:       &systemFunctionId,
			CurrentOperationModeId: &modeId,
		},
	}

	msgCounter, err := hvac.WriteHvacSystemFunctionListData(writeData)
	if err != nil {
		return msgCounter, err
	}
	return msgCounter, e.registerResultCallback(hvac, msgCounter, resultCB, func() {
		_, _ = hvac.RequestHvacSystemFunctions(nil, nil)
	})
}

// Scenario 2

// start the one-time DHW loading overrun of the DHW circuit,
// returns ErrNotSupported if the overrun status is not changeable.
//
// The returned message counter and the resultCB let the caller observe the
// device result: a non-zero ResultData.ErrorNumber signals a rejected write.
func (e *CDSF) StartOneTimeDhw(
	entity spineapi.EntityRemoteInterface,
	resultCB func(result model.ResultDataType, msgCounter model.MsgCounterType),
) (*model.MsgCounterType, error) {
	return e.writeOverrunStatus(entity, model.HvacOverrunStatusTypeActive, resultCB)
}

// Scenario 3

// stop the one-time DHW loading overrun of the DHW circuit,
// returns ErrNotSupported if the overrun status is not changeable.
//
// The returned message counter and the resultCB let the caller observe the
// device result: a non-zero ResultData.ErrorNumber signals a rejected write.
func (e *CDSF) StopOneTimeDhw(
	entity spineapi.EntityRemoteInterface,
	resultCB func(result model.ResultDataType, msgCounter model.MsgCounterType),
) (*model.MsgCounterType, error) {
	return e.writeOverrunStatus(entity, model.HvacOverrunStatusTypeInactive, resultCB)
}

// write the status of the one-time DHW overrun
func (e *CDSF) writeOverrunStatus(
	entity spineapi.EntityRemoteInterface,
	status model.HvacOverrunStatusType,
	resultCB func(result model.ResultDataType, msgCounter model.MsgCounterType),
) (*model.MsgCounterType, error) {
	if !e.IsCompatibleEntityType(entity) {
		return nil, api.ErrNoCompatibleEntity
	}

	hvac, err := client.NewHvac(e.LocalEntity, entity)
	if err != nil {
		return nil, err
	}

	overrunId, err := e.overrunId(entity)
	if err != nil {
		return nil, err
	}

	if data, err := hvac.GetHvacOverrunForId(overrunId); err == nil &&
		data.IsOverrunStatusChangeable != nil && !*data.IsOverrunStatusChangeable {
		return nil, api.ErrNotSupported
	}

	writeData := []model.HvacOverrunDataType{
		{
			OverrunId:     &overrunId,
			OverrunStatus: &status,
		},
	}

	msgCounter, err := hvac.WriteHvacOverrunListData(writeData)
	if err != nil {
		return msgCounter, err
	}
	return msgCounter, e.registerResultCallback(hvac, msgCounter, resultCB, func() {
		_, _ = hvac.RequestHvacOverruns(nil, nil)
	})
}

type responseCallbackRegistrar interface {
	AddResponseCallback(model.MsgCounterType, func(spineapi.ResponseMessage)) error
}

// register a response callback that surfaces the device result of a write to
// the caller, so a non-zero ResultData.ErrorNumber can be treated as a rejection
func (e *CDSF) registerResultCallback(
	registrar responseCallbackRegistrar,
	msgCounter *model.MsgCounterType,
	resultCB func(result model.ResultDataType, msgCounter model.MsgCounterType),
	refresh func(),
) error {
	if msgCounter == nil {
		return nil
	}

	cb := func(msg spineapi.ResponseMessage) {
		if response, ok := msg.Data.(*model.ResultDataType); ok {
			if (response.ErrorNumber == nil || *response.ErrorNumber == model.ErrorNumberTypeNoError) && refresh != nil {
				refresh()
			}
			if resultCB != nil {
				resultCB(*response, *msgCounter)
			}
		}
	}
	return registrar.AddResponseCallback(*msgCounter, cb)
}

// return the id of the one-time DHW overrun affecting the DHW system function,
// returns ErrDataNotAvailable unless exactly one matching overrun exists
func (e *CDSF) overrunId(entity spineapi.EntityRemoteInterface) (model.HvacOverrunIdType, error) {
	hvac, err := client.NewHvac(e.LocalEntity, entity)
	if err != nil {
		return 0, err
	}

	systemFunctionId, err := e.systemFunctionId(entity)
	if err != nil {
		return 0, err
	}

	descFilter := model.HvacOverrunDescriptionDataType{
		OverrunType: util.Ptr(model.HvacOverrunTypeTypeOneTimeDhw),
	}
	descriptions, err := hvac.GetHvacOverrunDescriptionsForFilter(descFilter)
	if err != nil {
		return 0, api.ErrDataNotAvailable
	}

	var overrunIds []model.HvacOverrunIdType
	for _, description := range descriptions {
		if description.OverrunId == nil {
			continue
		}

		for _, affectedId := range description.AffectedSystemFunctionId {
			if affectedId == systemFunctionId {
				overrunIds = append(overrunIds, *description.OverrunId)
				break
			}
		}
	}

	// fail closed on an ambiguous result so the wrong overrun is never controlled
	if len(overrunIds) != 1 {
		return 0, api.ErrDataNotAvailable
	}

	return overrunIds[0], nil
}

// return the id of the DHW system function of the DHW circuit,
// returns ErrDataNotAvailable unless exactly one matching system function exists
func (e *CDSF) systemFunctionId(entity spineapi.EntityRemoteInterface) (model.HvacSystemFunctionIdType, error) {
	hvac, err := client.NewHvac(e.LocalEntity, entity)
	if err != nil {
		return 0, err
	}

	descFilter := model.HvacSystemFunctionDescriptionDataType{
		SystemFunctionType: util.Ptr(model.HvacSystemFunctionTypeTypeDhw),
	}
	descriptions, err := hvac.GetHvacSystemFunctionDescriptionsForFilter(descFilter)
	// fail closed on an ambiguous result so the wrong system function is never controlled
	if err != nil || len(descriptions) != 1 || descriptions[0].SystemFunctionId == nil {
		return 0, api.ErrDataNotAvailable
	}

	return *descriptions[0].SystemFunctionId, nil
}
