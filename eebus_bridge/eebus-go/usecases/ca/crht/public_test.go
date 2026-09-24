package crht

import (
	"encoding/json"
	"errors"
	"math"

	"github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
	"github.com/stretchr/testify/assert"
)

func (s *CaCRHTSuite) Test_Setpoints() {
	data, err := s.sut.Setpoints(s.mockRemoteEntity)
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), data)

	data, err = s.sut.Setpoints(s.hvacRoomEntity)
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), data)

	s.addHvacData()

	data, err = s.sut.Setpoints(s.hvacRoomEntity)
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), data)

	s.addSetpointData()

	data, err = s.sut.Setpoints(s.hvacRoomEntity)
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), 2, len(data))
	assert.Equal(s.T(), uint(1), data[0].Id)
	assert.Equal(s.T(), 21.0, data[0].Value)
	assert.True(s.T(), data[0].IsChangeable)
	assert.True(s.T(), data[1].IsActive)
}

func (s *CaCRHTSuite) Test_SetpointConstraints() {
	data, err := s.sut.SetpointConstraints(s.mockRemoteEntity)
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), data)

	data, err = s.sut.SetpointConstraints(s.hvacRoomEntity)
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), data)

	s.addHvacData()
	s.addSetpointData()

	data, err = s.sut.SetpointConstraints(s.hvacRoomEntity)
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), 1, len(data))
	assert.Equal(s.T(), uint(1), data[0].Id)
	assert.Equal(s.T(), 16.0, data[0].MinValue)
	assert.Equal(s.T(), 25.0, data[0].MaxValue)
	assert.Equal(s.T(), 0.5, data[0].StepSize)
}

func (s *CaCRHTSuite) Test_StateReturnsCompleteDeduplicatedRoomAirSetpoint() {
	state, err := s.sut.State(s.mockRemoteEntity)
	assert.Error(s.T(), err)
	assert.Zero(s.T(), state)

	s.addCompleteRoomAirSetpointState()

	state, err = s.sut.State(s.hvacRoomEntity)
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), ucapi.RoomHeatingSetpointState{
		Id:           1,
		Value:        21,
		MinValue:     5,
		MaxValue:     30,
		StepSize:     0.5,
		IsActive:     true,
		IsChangeable: true,
		IsWritable:   true,
	}, state)
}

func (s *CaCRHTSuite) Test_StateRejectsMissingOrInvalidNumericFields() {
	tests := map[string]struct {
		value   *model.ScaledNumberType
		minimum *model.ScaledNumberType
		maximum *model.ScaledNumberType
		step    *model.ScaledNumberType
	}{
		"missing value":   {nil, model.NewScaledNumberType(5), model.NewScaledNumberType(30), model.NewScaledNumberType(0.5)},
		"missing minimum": {model.NewScaledNumberType(21), nil, model.NewScaledNumberType(30), model.NewScaledNumberType(0.5)},
		"missing maximum": {model.NewScaledNumberType(21), model.NewScaledNumberType(5), nil, model.NewScaledNumberType(0.5)},
		"missing step":    {model.NewScaledNumberType(21), model.NewScaledNumberType(5), model.NewScaledNumberType(30), nil},
		"zero step":       {model.NewScaledNumberType(21), model.NewScaledNumberType(5), model.NewScaledNumberType(30), model.NewScaledNumberType(0)},
		"reversed range":  {model.NewScaledNumberType(21), model.NewScaledNumberType(30), model.NewScaledNumberType(5), model.NewScaledNumberType(0.5)},
	}

	for name, test := range tests {
		s.Run(name, func() {
			s.addCompleteRoomAirSetpointState()
			s.updateSetpointState(test.value, test.minimum, test.maximum, test.step)

			state, err := s.sut.State(s.hvacRoomEntity)
			assert.ErrorIs(s.T(), err, api.ErrDataNotAvailable)
			assert.Zero(s.T(), state)
		})
	}
}

func (s *CaCRHTSuite) Test_StateRejectsSeveralDistinctRoomAirSetpoints() {
	s.addCompleteRoomAirSetpointState()

	hvacFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.hvacRoomEntity, model.FeatureTypeTypeHvac, model.RoleTypeServer)
	relations := &model.HvacSystemFunctionSetpointRelationListDataType{
		HvacSystemFunctionSetpointRelationData: []model.HvacSystemFunctionSetpointRelationDataType{
			{SystemFunctionId: util.Ptr(model.HvacSystemFunctionIdType(1)), SetpointId: []model.SetpointIdType{1}},
			{SystemFunctionId: util.Ptr(model.HvacSystemFunctionIdType(1)), SetpointId: []model.SetpointIdType{2}},
		},
	}
	_, fErr := hvacFeature.UpdateData(true, model.FunctionTypeHvacSystemFunctionSetPointRelationListData, relations, nil, nil)
	assert.Nil(s.T(), fErr)

	setpointFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.hvacRoomEntity, model.FeatureTypeTypeSetpoint, model.RoleTypeServer)
	descriptions := &model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
		{SetpointId: util.Ptr(model.SetpointIdType(1)), ScopeType: util.Ptr(model.ScopeTypeTypeRoomAirTemperature)},
		{SetpointId: util.Ptr(model.SetpointIdType(2)), ScopeType: util.Ptr(model.ScopeTypeTypeRoomAirTemperature)},
	}}
	_, fErr = setpointFeature.UpdateData(true, model.FunctionTypeSetpointDescriptionListData, descriptions, nil, nil)
	assert.Nil(s.T(), fErr)

	state, stateErr := s.sut.State(s.hvacRoomEntity)
	assert.ErrorIs(s.T(), stateErr, api.ErrDataNotAvailable)
	assert.Zero(s.T(), state)
}

func (s *CaCRHTSuite) Test_WriteSetpoint() {
	_, err := s.sut.WriteSetpoint(s.mockRemoteEntity, ucapi.HvacOperationModeTypeEco, 19, nil)
	assert.NotNil(s.T(), err)

	// the setpoints of the auto mode cannot be written
	_, err = s.sut.WriteSetpoint(s.hvacRoomEntity, ucapi.HvacOperationModeTypeAuto, 19, nil)
	assert.NotNil(s.T(), err)

	_, err = s.sut.WriteSetpoint(s.hvacRoomEntity, ucapi.HvacOperationModeTypeEco, 19, nil)
	assert.NotNil(s.T(), err)

	s.addHvacData()

	_, err = s.sut.WriteSetpoint(s.hvacRoomEntity, ucapi.HvacOperationModeTypeEco, 19, nil)
	assert.Nil(s.T(), err)

	// the off mode has no setpoint in the test data
	_, err = s.sut.WriteSetpoint(s.hvacRoomEntity, ucapi.HvacOperationModeTypeOff, 19, nil)
	assert.NotNil(s.T(), err)

	// a setpoint marked not changeable cannot be written
	s.addSetpointData()

	_, err = s.sut.WriteSetpoint(s.hvacRoomEntity, ucapi.HvacOperationModeTypeOn, 22, nil)
	assert.NotNil(s.T(), err)

	_, err = s.sut.WriteSetpoint(s.hvacRoomEntity, ucapi.HvacOperationModeTypeEco, 19, nil)
	assert.Nil(s.T(), err)
}

func (s *CaCRHTSuite) Test_WriteSetpoint_AmbiguousSystemFunction() {
	s.addHvacData()

	// two heating system functions make the id ambiguous, so the write must fail
	rFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.hvacRoomEntity, model.FeatureTypeTypeHvac, model.RoleTypeServer)
	descData := &model.HvacSystemFunctionDescriptionListDataType{
		HvacSystemFunctionDescriptionData: []model.HvacSystemFunctionDescriptionDataType{
			{
				SystemFunctionId:   util.Ptr(model.HvacSystemFunctionIdType(1)),
				SystemFunctionType: util.Ptr(model.HvacSystemFunctionTypeTypeHeating),
			},
			{
				SystemFunctionId:   util.Ptr(model.HvacSystemFunctionIdType(2)),
				SystemFunctionType: util.Ptr(model.HvacSystemFunctionTypeTypeHeating),
			},
		},
	}
	_, fErr := rFeature.UpdateData(true, model.FunctionTypeHvacSystemFunctionDescriptionListData, descData, nil, nil)
	assert.Nil(s.T(), fErr)

	_, err := s.sut.WriteSetpoint(s.hvacRoomEntity, ucapi.HvacOperationModeTypeEco, 19, nil)
	assert.NotNil(s.T(), err)
}

func (s *CaCRHTSuite) Test_WriteSetpoint_WriteNotAdvertised() {
	s.addHvacData()

	// re-advertise the setpoint list as read-only
	rFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.hvacRoomEntity, model.FeatureTypeTypeSetpoint, model.RoleTypeServer)
	rFeature.SetOperations([]model.FunctionPropertyType{
		{
			Function:           util.Ptr(model.FunctionTypeSetpointListData),
			PossibleOperations: &model.PossibleOperationsType{Read: &model.PossibleOperationsReadType{}},
		},
	})

	// the write must be rejected when the remote does not advertise Write()
	_, err := s.sut.WriteSetpoint(s.hvacRoomEntity, ucapi.HvacOperationModeTypeEco, 19, nil)
	assert.ErrorIs(s.T(), err, api.ErrNotSupported)
}

func (s *CaCRHTSuite) Test_WriteRoomAirTemperatureSetpoint() {
	_, err := s.sut.WriteRoomAirTemperatureSetpoint(s.mockRemoteEntity, 21.5, nil)
	assert.ErrorIs(s.T(), err, api.ErrNoCompatibleEntity)

	_, err = s.sut.WriteRoomAirTemperatureSetpoint(s.hvacRoomEntity, 21.5, nil)
	assert.ErrorIs(s.T(), err, api.ErrDataNotAvailable)

	s.addCompleteRoomAirSetpointState()
	setpointFeature := s.remoteDevice.FeatureByEntityTypeAndRole(
		s.hvacRoomEntity, model.FeatureTypeTypeSetpoint, model.RoleTypeServer,
	)
	setpoints := &model.SetpointListDataType{SetpointData: []model.SetpointDataType{
		{SetpointId: util.Ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(21)},
		{SetpointId: util.Ptr(model.SetpointIdType(99)), Value: model.NewScaledNumberType(7)},
	}}
	_, updateErr := setpointFeature.UpdateData(true, model.FunctionTypeSetpointListData, setpoints, nil, nil)
	assert.Nil(s.T(), updateErr)
	msgCounter, err := s.sut.WriteRoomAirTemperatureSetpoint(s.hvacRoomEntity, 21.5, nil)
	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), msgCounter)

	var datagram model.Datagram
	assert.NoError(s.T(), json.Unmarshal(s.sentBytes, &datagram))
	written := datagram.Datagram.Payload.Cmd[0].SetpointListData.SetpointData
	assert.Len(s.T(), written, 2)
	assert.Equal(s.T(), 21.5, written[0].Value.GetValue())
	assert.Equal(s.T(), 7.0, written[1].Value.GetValue())

	for _, value := range []float64{4.5, 30.5, 21.25, math.NaN(), math.Inf(1)} {
		_, err = s.sut.WriteRoomAirTemperatureSetpoint(s.hvacRoomEntity, value, nil)
		assert.ErrorIs(s.T(), err, api.ErrDataInvalid, "value %v", value)
	}
}

func (s *CaCRHTSuite) Test_WriteRoomAirTemperatureSetpointRejectsReadOnlyOrUnchangeableState() {
	s.addCompleteRoomAirSetpointState()
	setpointFeature := s.remoteDevice.FeatureByEntityTypeAndRole(
		s.hvacRoomEntity, model.FeatureTypeTypeSetpoint, model.RoleTypeServer,
	)
	setpointFeature.SetOperations([]model.FunctionPropertyType{{
		Function:           util.Ptr(model.FunctionTypeSetpointListData),
		PossibleOperations: &model.PossibleOperationsType{Read: &model.PossibleOperationsReadType{}},
	}})
	_, err := s.sut.WriteRoomAirTemperatureSetpoint(s.hvacRoomEntity, 21.5, nil)
	assert.ErrorIs(s.T(), err, api.ErrNotSupported)

	setpointFeature.SetOperations([]model.FunctionPropertyType{{
		Function: util.Ptr(model.FunctionTypeSetpointListData),
		PossibleOperations: &model.PossibleOperationsType{
			Read:  &model.PossibleOperationsReadType{},
			Write: &model.PossibleOperationsWriteType{},
		},
	}})
	setpoints := &model.SetpointListDataType{SetpointData: []model.SetpointDataType{{
		SetpointId:           util.Ptr(model.SetpointIdType(1)),
		Value:                model.NewScaledNumberType(21),
		IsSetpointChangeable: util.Ptr(false),
	}}}
	_, updateErr := setpointFeature.UpdateData(true, model.FunctionTypeSetpointListData, setpoints, nil, nil)
	assert.Nil(s.T(), updateErr)
	_, err = s.sut.WriteRoomAirTemperatureSetpoint(s.hvacRoomEntity, 21.5, nil)
	assert.ErrorIs(s.T(), err, api.ErrNotSupported)
}

type crhtResultHandlerStub struct {
	callback    func(spineapi.ResponseMessage)
	registerErr error
	requests    int
}

func (s *crhtResultHandlerStub) AddResponseCallback(
	_ model.MsgCounterType,
	callback func(spineapi.ResponseMessage),
) error {
	s.callback = callback
	return s.registerErr
}

func (s *crhtResultHandlerStub) RequestSetpoints(
	*model.SetpointListDataSelectorsType,
	*model.SetpointDataElementsType,
) (*model.MsgCounterType, error) {
	s.requests++
	return nil, nil
}

func (s *CaCRHTSuite) Test_RegisterSetpointResultCallbackRefreshesBeforeAcceptedCallback() {
	counter := model.MsgCounterType(23)
	handler := &crhtResultHandlerStub{}
	callbackRequests := -1
	err := registerSetpointResultCallback(handler, &counter, func(model.ResultDataType, model.MsgCounterType) {
		callbackRequests = handler.requests
	})
	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), handler.callback)

	noError := model.ErrorNumberTypeNoError
	handler.callback(spineapi.ResponseMessage{Data: &model.ResultDataType{ErrorNumber: &noError}})
	assert.Equal(s.T(), 1, handler.requests)
	assert.Equal(s.T(), 1, callbackRequests)

	rejected := model.ErrorNumberTypeCommandRejected
	handler.callback(spineapi.ResponseMessage{Data: &model.ResultDataType{ErrorNumber: &rejected}})
	assert.Equal(s.T(), 1, handler.requests)
	assert.Equal(s.T(), 1, callbackRequests)
}

func (s *CaCRHTSuite) Test_RegisterSetpointResultCallbackReportsRegistrationFailure() {
	counter := model.MsgCounterType(24)
	registerErr := errors.New("register failed")
	handler := &crhtResultHandlerStub{registerErr: registerErr}
	err := registerSetpointResultCallback(handler, &counter, nil)
	assert.ErrorIs(s.T(), err, registerErr)
}

// helpers

func (s *CaCRHTSuite) addHvacData() {
	rFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.hvacRoomEntity, model.FeatureTypeTypeHvac, model.RoleTypeServer)

	descData := &model.HvacSystemFunctionDescriptionListDataType{
		HvacSystemFunctionDescriptionData: []model.HvacSystemFunctionDescriptionDataType{
			{
				SystemFunctionId:   util.Ptr(model.HvacSystemFunctionIdType(1)),
				SystemFunctionType: util.Ptr(model.HvacSystemFunctionTypeTypeHeating),
			},
		},
	}
	_, fErr := rFeature.UpdateData(true, model.FunctionTypeHvacSystemFunctionDescriptionListData, descData, nil, nil)
	assert.Nil(s.T(), fErr)

	modeData := &model.HvacOperationModeDescriptionListDataType{
		HvacOperationModeDescriptionData: []model.HvacOperationModeDescriptionDataType{
			{
				OperationModeId:   util.Ptr(model.HvacOperationModeIdType(1)),
				OperationModeType: util.Ptr(model.HvacOperationModeTypeTypeEco),
			},
			{
				OperationModeId:   util.Ptr(model.HvacOperationModeIdType(2)),
				OperationModeType: util.Ptr(model.HvacOperationModeTypeTypeOn),
			},
			{
				OperationModeId:   util.Ptr(model.HvacOperationModeIdType(3)),
				OperationModeType: util.Ptr(model.HvacOperationModeTypeTypeOff),
			},
		},
	}
	_, fErr = rFeature.UpdateData(true, model.FunctionTypeHvacOperationModeDescriptionListData, modeData, nil, nil)
	assert.Nil(s.T(), fErr)

	relationData := &model.HvacSystemFunctionSetpointRelationListDataType{
		HvacSystemFunctionSetpointRelationData: []model.HvacSystemFunctionSetpointRelationDataType{
			{
				SystemFunctionId: util.Ptr(model.HvacSystemFunctionIdType(1)),
				OperationModeId:  util.Ptr(model.HvacOperationModeIdType(1)),
				SetpointId:       []model.SetpointIdType{1},
			},
			{
				SystemFunctionId: util.Ptr(model.HvacSystemFunctionIdType(1)),
				OperationModeId:  util.Ptr(model.HvacOperationModeIdType(2)),
				SetpointId:       []model.SetpointIdType{2},
			},
		},
	}
	_, fErr = rFeature.UpdateData(true, model.FunctionTypeHvacSystemFunctionSetPointRelationListData, relationData, nil, nil)
	assert.Nil(s.T(), fErr)
}

func (s *CaCRHTSuite) addSetpointData() {
	rFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.hvacRoomEntity, model.FeatureTypeTypeSetpoint, model.RoleTypeServer)

	spData := &model.SetpointListDataType{
		SetpointData: []model.SetpointDataType{
			{
				SetpointId:           util.Ptr(model.SetpointIdType(1)),
				Value:                model.NewScaledNumberType(21),
				ValueMin:             model.NewScaledNumberType(16),
				ValueMax:             model.NewScaledNumberType(25),
				IsSetpointChangeable: util.Ptr(true),
			},
			{
				SetpointId:           util.Ptr(model.SetpointIdType(2)),
				Value:                model.NewScaledNumberType(23),
				IsSetpointActive:     util.Ptr(true),
				IsSetpointChangeable: util.Ptr(false),
			},
		},
	}
	_, fErr := rFeature.UpdateData(true, model.FunctionTypeSetpointListData, spData, nil, nil)
	assert.Nil(s.T(), fErr)

	constraintsData := &model.SetpointConstraintsListDataType{
		SetpointConstraintsData: []model.SetpointConstraintsDataType{
			{
				SetpointId:       util.Ptr(model.SetpointIdType(1)),
				SetpointRangeMin: model.NewScaledNumberType(16),
				SetpointRangeMax: model.NewScaledNumberType(25),
				SetpointStepSize: model.NewScaledNumberType(0.5),
			},
		},
	}
	_, fErr = rFeature.UpdateData(true, model.FunctionTypeSetpointConstraintsListData, constraintsData, nil, nil)
	assert.Nil(s.T(), fErr)
}

func (s *CaCRHTSuite) addCompleteRoomAirSetpointState() {
	s.addHvacData()

	hvacFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.hvacRoomEntity, model.FeatureTypeTypeHvac, model.RoleTypeServer)
	relations := &model.HvacSystemFunctionSetpointRelationListDataType{
		HvacSystemFunctionSetpointRelationData: []model.HvacSystemFunctionSetpointRelationDataType{
			{
				SystemFunctionId: util.Ptr(model.HvacSystemFunctionIdType(1)),
				OperationModeId:  util.Ptr(model.HvacOperationModeIdType(1)),
				SetpointId:       []model.SetpointIdType{1},
			},
			{
				SystemFunctionId: util.Ptr(model.HvacSystemFunctionIdType(1)),
				OperationModeId:  util.Ptr(model.HvacOperationModeIdType(2)),
				SetpointId:       []model.SetpointIdType{1},
			},
		},
	}
	_, fErr := hvacFeature.UpdateData(true, model.FunctionTypeHvacSystemFunctionSetPointRelationListData, relations, nil, nil)
	assert.Nil(s.T(), fErr)

	setpointFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.hvacRoomEntity, model.FeatureTypeTypeSetpoint, model.RoleTypeServer)
	descriptions := &model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{{
		SetpointId: util.Ptr(model.SetpointIdType(1)),
		ScopeType:  util.Ptr(model.ScopeTypeTypeRoomAirTemperature),
	}}}
	_, fErr = setpointFeature.UpdateData(true, model.FunctionTypeSetpointDescriptionListData, descriptions, nil, nil)
	assert.Nil(s.T(), fErr)

	s.updateSetpointState(
		model.NewScaledNumberType(21),
		model.NewScaledNumberType(5),
		model.NewScaledNumberType(30),
		model.NewScaledNumberType(0.5),
	)
}

func (s *CaCRHTSuite) updateSetpointState(
	value *model.ScaledNumberType,
	minimum *model.ScaledNumberType,
	maximum *model.ScaledNumberType,
	step *model.ScaledNumberType,
) {
	setpointFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.hvacRoomEntity, model.FeatureTypeTypeSetpoint, model.RoleTypeServer)
	setpoints := &model.SetpointListDataType{SetpointData: []model.SetpointDataType{{
		SetpointId: util.Ptr(model.SetpointIdType(1)),
		Value:      value,
	}}}
	_, fErr := setpointFeature.UpdateData(true, model.FunctionTypeSetpointListData, setpoints, nil, nil)
	assert.Nil(s.T(), fErr)
	constraints := &model.SetpointConstraintsListDataType{SetpointConstraintsData: []model.SetpointConstraintsDataType{{
		SetpointId:       util.Ptr(model.SetpointIdType(1)),
		SetpointRangeMin: minimum,
		SetpointRangeMax: maximum,
		SetpointStepSize: step,
	}}}
	_, fErr = setpointFeature.UpdateData(true, model.FunctionTypeSetpointConstraintsListData, constraints, nil, nil)
	assert.Nil(s.T(), fErr)
}
