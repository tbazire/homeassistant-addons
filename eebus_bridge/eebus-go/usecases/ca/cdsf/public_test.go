package cdsf

import (
	"errors"
	"testing"

	"github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
	"github.com/stretchr/testify/assert"
)

func (s *CaCDSFSuite) Test_OperationModes() {
	data, err := s.sut.OperationModes(s.mockRemoteEntity)
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), data)

	data, err = s.sut.OperationModes(s.dhwCircuitEntity)
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), data)

	s.addHvacData(util.Ptr(true))

	data, err = s.sut.OperationModes(s.dhwCircuitEntity)
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), 3, len(data))
	assert.Equal(s.T(), ucapi.HvacOperationModeTypeAuto, data[0])
}

func (s *CaCDSFSuite) Test_CurrentOperationMode() {
	data, err := s.sut.CurrentOperationMode(s.mockRemoteEntity)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), ucapi.HvacOperationModeType(""), data)

	data, err = s.sut.CurrentOperationMode(s.dhwCircuitEntity)
	assert.NotNil(s.T(), err)
	assert.Equal(s.T(), ucapi.HvacOperationModeType(""), data)

	s.addHvacData(util.Ptr(true))

	data, err = s.sut.CurrentOperationMode(s.dhwCircuitEntity)
	assert.Nil(s.T(), err)
	assert.Equal(s.T(), ucapi.HvacOperationModeTypeEco, data)
}

func (s *CaCDSFSuite) Test_WriteCapabilities() {
	capabilities, err := s.sut.WriteCapabilities(s.mockRemoteEntity)
	assert.ErrorIs(s.T(), err, api.ErrNoCompatibleEntity)
	assert.Equal(s.T(), ucapi.DHWSystemFunctionWriteCapabilities{}, capabilities)

	s.addHvacData(util.Ptr(true))
	s.addOverrunData(true)
	s.setSupportedScenarios(1, 2, 3)

	capabilities, err = s.sut.WriteCapabilities(s.dhwCircuitEntity)
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), ucapi.DHWSystemFunctionWriteCapabilities{
		OperationMode:   true,
		StartOneTimeDhw: true,
		StopOneTimeDhw:  true,
	}, capabilities)

	// Start and stop are deliberately independent because they are separate
	// scenarios even though both operate on the same overrun data.
	s.setSupportedScenarios(1, 2)
	capabilities, err = s.sut.WriteCapabilities(s.dhwCircuitEntity)
	assert.NoError(s.T(), err)
	assert.True(s.T(), capabilities.OperationMode)
	assert.True(s.T(), capabilities.StartOneTimeDhw)
	assert.False(s.T(), capabilities.StopOneTimeDhw)
}

func (s *CaCDSFSuite) Test_WriteCapabilitiesFailClosed() {
	s.addHvacData(util.Ptr(false))
	s.addOverrunData(false)
	s.setSupportedScenarios(1, 2, 3)

	capabilities, err := s.sut.WriteCapabilities(s.dhwCircuitEntity)
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), ucapi.DHWSystemFunctionWriteCapabilities{}, capabilities)

	// Missing or ambiguous metadata fails only the affected capability closed.
	rFeature := s.remoteDevice.FeatureByEntityTypeAndRole(
		s.dhwCircuitEntity, model.FeatureTypeTypeHvac, model.RoleTypeServer,
	)
	_, updateErr := rFeature.UpdateData(
		true,
		model.FunctionTypeHvacOverrunDescriptionListData,
		&model.HvacOverrunDescriptionListDataType{},
		nil,
		nil,
	)
	assert.Nil(s.T(), updateErr)
	capabilities, err = s.sut.WriteCapabilities(s.dhwCircuitEntity)
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), ucapi.DHWSystemFunctionWriteCapabilities{}, capabilities)
}

func (s *CaCDSFSuite) Test_WriteCapabilitiesResolveModeAndBoostIndependently() {
	s.addHvacData(util.Ptr(true))
	s.addOverrunData(true)
	s.setSupportedScenarios(1, 2, 3)
	rFeature := s.remoteDevice.FeatureByEntityTypeAndRole(
		s.dhwCircuitEntity, model.FeatureTypeTypeHvac, model.RoleTypeServer,
	)

	_, updateErr := rFeature.UpdateData(
		true,
		model.FunctionTypeHvacOverrunDescriptionListData,
		&model.HvacOverrunDescriptionListDataType{},
		nil,
		nil,
	)
	assert.Nil(s.T(), updateErr)
	capabilities, err := s.sut.WriteCapabilities(s.dhwCircuitEntity)
	assert.NoError(s.T(), err)
	assert.True(s.T(), capabilities.OperationMode)
	assert.False(s.T(), capabilities.StartOneTimeDhw)
	assert.False(s.T(), capabilities.StopOneTimeDhw)

	s.addOverrunData(true)
	_, updateErr = rFeature.UpdateData(
		true,
		model.FunctionTypeHvacSystemFunctionOperationModeRelationListData,
		&model.HvacSystemFunctionOperationModeRelationListDataType{},
		nil,
		nil,
	)
	assert.Nil(s.T(), updateErr)
	capabilities, err = s.sut.WriteCapabilities(s.dhwCircuitEntity)
	assert.NoError(s.T(), err)
	assert.False(s.T(), capabilities.OperationMode)
	assert.True(s.T(), capabilities.StartOneTimeDhw)
	assert.True(s.T(), capabilities.StopOneTimeDhw)
}

func (s *CaCDSFSuite) Test_WriteOperationMode() {
	_, err := s.sut.WriteOperationMode(s.mockRemoteEntity, ucapi.HvacOperationModeTypeOn, nil)
	assert.NotNil(s.T(), err)

	_, err = s.sut.WriteOperationMode(s.dhwCircuitEntity, ucapi.HvacOperationModeTypeOn, nil)
	assert.NotNil(s.T(), err)

	s.addHvacData(util.Ptr(true))

	_, err = s.sut.WriteOperationMode(s.dhwCircuitEntity, ucapi.HvacOperationModeTypeOn, nil)
	assert.Nil(s.T(), err)

	// an unsupported mode cannot be written
	_, err = s.sut.WriteOperationMode(s.dhwCircuitEntity, ucapi.HvacOperationModeType("invalid"), nil)
	assert.NotNil(s.T(), err)
}

func (s *CaCDSFSuite) Test_WriteOperationMode_NotChangeable() {
	s.addHvacData(util.Ptr(false))

	_, err := s.sut.WriteOperationMode(s.dhwCircuitEntity, ucapi.HvacOperationModeTypeOn, nil)
	assert.NotNil(s.T(), err)
}

func (s *CaCDSFSuite) Test_WriteOperationMode_ChangeabilityOmitted() {
	// a device may omit the changeability flag but still accept the write
	s.addHvacData(nil)

	_, err := s.sut.WriteOperationMode(s.dhwCircuitEntity, ucapi.HvacOperationModeTypeOn, nil)
	assert.Nil(s.T(), err)
}

func (s *CaCDSFSuite) Test_WriteOperationMode_UnrelatedMode() {
	s.addHvacData(util.Ptr(true))

	// add a mode that exists globally but is not related to the DHW function
	rFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.dhwCircuitEntity, model.FeatureTypeTypeHvac, model.RoleTypeServer)
	modeData := &model.HvacOperationModeDescriptionListDataType{
		HvacOperationModeDescriptionData: []model.HvacOperationModeDescriptionDataType{
			{
				OperationModeId:   util.Ptr(model.HvacOperationModeIdType(1)),
				OperationModeType: util.Ptr(model.HvacOperationModeTypeTypeAuto),
			},
			{
				OperationModeId:   util.Ptr(model.HvacOperationModeIdType(2)),
				OperationModeType: util.Ptr(model.HvacOperationModeTypeTypeOn),
			},
			{
				OperationModeId:   util.Ptr(model.HvacOperationModeIdType(3)),
				OperationModeType: util.Ptr(model.HvacOperationModeTypeTypeEco),
			},
			{
				OperationModeId:   util.Ptr(model.HvacOperationModeIdType(4)),
				OperationModeType: util.Ptr(model.HvacOperationModeTypeTypeOff),
			},
		},
	}
	_, fErr := rFeature.UpdateData(true, model.FunctionTypeHvacOperationModeDescriptionListData, modeData, nil, nil)
	assert.Nil(s.T(), fErr)

	// the relation only lists modes 1, 2, 3, so the unrelated mode must not be written
	_, err := s.sut.WriteOperationMode(s.dhwCircuitEntity, ucapi.HvacOperationModeTypeOff, nil)
	assert.NotNil(s.T(), err)
}

func (s *CaCDSFSuite) Test_WriteOperationMode_AmbiguousRelatedModeType() {
	s.addHvacData(util.Ptr(true))
	rFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.dhwCircuitEntity, model.FeatureTypeTypeHvac, model.RoleTypeServer)

	modeData := &model.HvacOperationModeDescriptionListDataType{
		HvacOperationModeDescriptionData: []model.HvacOperationModeDescriptionDataType{
			{OperationModeId: util.Ptr(model.HvacOperationModeIdType(2)), OperationModeType: util.Ptr(model.HvacOperationModeTypeTypeOn)},
			{OperationModeId: util.Ptr(model.HvacOperationModeIdType(4)), OperationModeType: util.Ptr(model.HvacOperationModeTypeTypeOn)},
		},
	}
	_, fErr := rFeature.UpdateData(true, model.FunctionTypeHvacOperationModeDescriptionListData, modeData, nil, nil)
	assert.Nil(s.T(), fErr)
	relationData := &model.HvacSystemFunctionOperationModeRelationListDataType{
		HvacSystemFunctionOperationModeRelationData: []model.HvacSystemFunctionOperationModeRelationDataType{{
			SystemFunctionId: util.Ptr(model.HvacSystemFunctionIdType(1)),
			OperationModeId:  []model.HvacOperationModeIdType{2, 4},
		}},
	}
	_, fErr = rFeature.UpdateData(true, model.FunctionTypeHvacSystemFunctionOperationModeRelationListData, relationData, nil, nil)
	assert.Nil(s.T(), fErr)

	_, err := s.sut.WriteOperationMode(s.dhwCircuitEntity, ucapi.HvacOperationModeTypeOn, nil)
	assert.ErrorIs(s.T(), err, api.ErrDataNotAvailable)
}

func (s *CaCDSFSuite) Test_StartStopOneTimeDhw() {
	_, err := s.sut.StartOneTimeDhw(s.mockRemoteEntity, nil)
	assert.NotNil(s.T(), err)

	_, err = s.sut.StartOneTimeDhw(s.dhwCircuitEntity, nil)
	assert.NotNil(s.T(), err)

	s.addHvacData(util.Ptr(true))

	_, err = s.sut.StartOneTimeDhw(s.dhwCircuitEntity, nil)
	assert.NotNil(s.T(), err)

	s.addOverrunData(true)

	_, err = s.sut.StartOneTimeDhw(s.dhwCircuitEntity, nil)
	assert.Nil(s.T(), err)

	_, err = s.sut.StopOneTimeDhw(s.dhwCircuitEntity, nil)
	assert.Nil(s.T(), err)

	// the overrun status marked not changeable cannot be written
	s.addOverrunData(false)

	_, err = s.sut.StartOneTimeDhw(s.dhwCircuitEntity, nil)
	assert.NotNil(s.T(), err)
}

func (s *CaCDSFSuite) Test_WriteOperationMode_AmbiguousSystemFunction() {
	s.addHvacData(util.Ptr(true))

	// two DHW system functions make the id ambiguous, so the write must fail
	rFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.dhwCircuitEntity, model.FeatureTypeTypeHvac, model.RoleTypeServer)
	descData := &model.HvacSystemFunctionDescriptionListDataType{
		HvacSystemFunctionDescriptionData: []model.HvacSystemFunctionDescriptionDataType{
			{
				SystemFunctionId:   util.Ptr(model.HvacSystemFunctionIdType(1)),
				SystemFunctionType: util.Ptr(model.HvacSystemFunctionTypeTypeDhw),
			},
			{
				SystemFunctionId:   util.Ptr(model.HvacSystemFunctionIdType(2)),
				SystemFunctionType: util.Ptr(model.HvacSystemFunctionTypeTypeDhw),
			},
		},
	}
	_, fErr := rFeature.UpdateData(true, model.FunctionTypeHvacSystemFunctionDescriptionListData, descData, nil, nil)
	assert.Nil(s.T(), fErr)

	_, err := s.sut.WriteOperationMode(s.dhwCircuitEntity, ucapi.HvacOperationModeTypeOn, nil)
	assert.NotNil(s.T(), err)
}

func (s *CaCDSFSuite) Test_WriteOperationMode_WriteNotAdvertised() {
	s.addHvacData(util.Ptr(true))

	// re-advertise the system-function list as read-only
	rFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.dhwCircuitEntity, model.FeatureTypeTypeHvac, model.RoleTypeServer)
	rFeature.SetOperations([]model.FunctionPropertyType{
		{
			Function:           util.Ptr(model.FunctionTypeHvacSystemFunctionListData),
			PossibleOperations: &model.PossibleOperationsType{Read: &model.PossibleOperationsReadType{}},
		},
	})

	// the write must be rejected when the remote does not advertise Write()
	_, err := s.sut.WriteOperationMode(s.dhwCircuitEntity, ucapi.HvacOperationModeTypeOn, nil)
	assert.ErrorIs(s.T(), err, api.ErrNotSupported)
}

func (s *CaCDSFSuite) Test_WriteOverrun_WriteNotAdvertised() {
	s.addHvacData(util.Ptr(true))
	s.addOverrunData(true)
	rFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.dhwCircuitEntity, model.FeatureTypeTypeHvac, model.RoleTypeServer)
	rFeature.SetOperations([]model.FunctionPropertyType{{
		Function:           util.Ptr(model.FunctionTypeHvacOverrunListData),
		PossibleOperations: &model.PossibleOperationsType{Read: &model.PossibleOperationsReadType{}},
	}})

	_, err := s.sut.StartOneTimeDhw(s.dhwCircuitEntity, nil)
	assert.ErrorIs(s.T(), err, api.ErrNotSupported)
}

func (s *CaCDSFSuite) Test_WriteOverrun_AmbiguousOverrun() {
	s.addHvacData(util.Ptr(true))
	s.addOverrunData(true)
	rFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.dhwCircuitEntity, model.FeatureTypeTypeHvac, model.RoleTypeServer)
	descriptions := &model.HvacOverrunDescriptionListDataType{
		HvacOverrunDescriptionData: []model.HvacOverrunDescriptionDataType{
			{OverrunId: util.Ptr(model.HvacOverrunIdType(1)), OverrunType: util.Ptr(model.HvacOverrunTypeTypeOneTimeDhw), AffectedSystemFunctionId: []model.HvacSystemFunctionIdType{1}},
			{OverrunId: util.Ptr(model.HvacOverrunIdType(2)), OverrunType: util.Ptr(model.HvacOverrunTypeTypeOneTimeDhw), AffectedSystemFunctionId: []model.HvacSystemFunctionIdType{1}},
		},
	}
	_, fErr := rFeature.UpdateData(true, model.FunctionTypeHvacOverrunDescriptionListData, descriptions, nil, nil)
	assert.Nil(s.T(), fErr)

	_, err := s.sut.StartOneTimeDhw(s.dhwCircuitEntity, nil)
	assert.ErrorIs(s.T(), err, api.ErrDataNotAvailable)
}

type responseCallbackRegistrarStub struct {
	callback func(spineapi.ResponseMessage)
	err      error
}

func (s *responseCallbackRegistrarStub) AddResponseCallback(
	_ model.MsgCounterType,
	callback func(spineapi.ResponseMessage),
) error {
	s.callback = callback
	return s.err
}

func TestRegisterResultCallbackForwardsDeviceResult(t *testing.T) {
	registrar := &responseCallbackRegistrarStub{}
	counter := model.MsgCounterType(42)
	errorNumber := model.ErrorNumberTypeCommandRejected
	called := false

	err := (&CDSF{}).registerResultCallback(registrar, &counter, func(result model.ResultDataType, gotCounter model.MsgCounterType) {
		called = true
		assert.Equal(t, counter, gotCounter)
		assert.Equal(t, errorNumber, *result.ErrorNumber)
	}, nil)
	assert.NoError(t, err)
	assert.NotNil(t, registrar.callback)
	registrar.callback(spineapi.ResponseMessage{Data: &model.ResultDataType{ErrorNumber: &errorNumber}})
	assert.True(t, called)
}

func TestRegisterResultCallbackRefreshesOnlyAcceptedWrites(t *testing.T) {
	registrar := &responseCallbackRegistrarStub{}
	counter := model.MsgCounterType(43)
	refreshes := 0

	err := (&CDSF{}).registerResultCallback(registrar, &counter, nil, func() { refreshes++ })
	assert.NoError(t, err)
	assert.NotNil(t, registrar.callback)

	noError := model.ErrorNumberTypeNoError
	registrar.callback(spineapi.ResponseMessage{Data: &model.ResultDataType{ErrorNumber: &noError}})
	assert.Equal(t, 1, refreshes)

	rejected := model.ErrorNumberTypeCommandRejected
	registrar.callback(spineapi.ResponseMessage{Data: &model.ResultDataType{ErrorNumber: &rejected}})
	assert.Equal(t, 1, refreshes)
}

func TestRegisterResultCallbackReturnsRegistrationError(t *testing.T) {
	want := errors.New("callback unavailable")
	registrar := &responseCallbackRegistrarStub{err: want}
	counter := model.MsgCounterType(42)

	err := (&CDSF{}).registerResultCallback(registrar, &counter, func(model.ResultDataType, model.MsgCounterType) {}, nil)
	assert.ErrorIs(t, err, want)
}

// helpers

func (s *CaCDSFSuite) addOverrunData(isChangeable bool) {
	rFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.dhwCircuitEntity, model.FeatureTypeTypeHvac, model.RoleTypeServer)

	descData := &model.HvacOverrunDescriptionListDataType{
		HvacOverrunDescriptionData: []model.HvacOverrunDescriptionDataType{
			{
				OverrunId:                util.Ptr(model.HvacOverrunIdType(1)),
				OverrunType:              util.Ptr(model.HvacOverrunTypeTypeOneTimeDhw),
				AffectedSystemFunctionId: []model.HvacSystemFunctionIdType{1},
			},
		},
	}
	_, fErr := rFeature.UpdateData(true, model.FunctionTypeHvacOverrunDescriptionListData, descData, nil, nil)
	assert.Nil(s.T(), fErr)

	overrunData := &model.HvacOverrunListDataType{
		HvacOverrunData: []model.HvacOverrunDataType{
			{
				OverrunId:                 util.Ptr(model.HvacOverrunIdType(1)),
				OverrunStatus:             util.Ptr(model.HvacOverrunStatusTypeInactive),
				IsOverrunStatusChangeable: util.Ptr(isChangeable),
			},
		},
	}
	_, fErr = rFeature.UpdateData(true, model.FunctionTypeHvacOverrunListData, overrunData, nil, nil)
	assert.Nil(s.T(), fErr)
}

func (s *CaCDSFSuite) addHvacData(isChangeable *bool) {
	rFeature := s.remoteDevice.FeatureByEntityTypeAndRole(s.dhwCircuitEntity, model.FeatureTypeTypeHvac, model.RoleTypeServer)

	descData := &model.HvacSystemFunctionDescriptionListDataType{
		HvacSystemFunctionDescriptionData: []model.HvacSystemFunctionDescriptionDataType{
			{
				SystemFunctionId:   util.Ptr(model.HvacSystemFunctionIdType(1)),
				SystemFunctionType: util.Ptr(model.HvacSystemFunctionTypeTypeDhw),
			},
		},
	}
	_, fErr := rFeature.UpdateData(true, model.FunctionTypeHvacSystemFunctionDescriptionListData, descData, nil, nil)
	assert.Nil(s.T(), fErr)

	modeData := &model.HvacOperationModeDescriptionListDataType{
		HvacOperationModeDescriptionData: []model.HvacOperationModeDescriptionDataType{
			{
				OperationModeId:   util.Ptr(model.HvacOperationModeIdType(1)),
				OperationModeType: util.Ptr(model.HvacOperationModeTypeTypeAuto),
			},
			{
				OperationModeId:   util.Ptr(model.HvacOperationModeIdType(2)),
				OperationModeType: util.Ptr(model.HvacOperationModeTypeTypeOn),
			},
			{
				OperationModeId:   util.Ptr(model.HvacOperationModeIdType(3)),
				OperationModeType: util.Ptr(model.HvacOperationModeTypeTypeEco),
			},
		},
	}
	_, fErr = rFeature.UpdateData(true, model.FunctionTypeHvacOperationModeDescriptionListData, modeData, nil, nil)
	assert.Nil(s.T(), fErr)

	relationData := &model.HvacSystemFunctionOperationModeRelationListDataType{
		HvacSystemFunctionOperationModeRelationData: []model.HvacSystemFunctionOperationModeRelationDataType{
			{
				SystemFunctionId: util.Ptr(model.HvacSystemFunctionIdType(1)),
				OperationModeId:  []model.HvacOperationModeIdType{1, 2, 3},
			},
		},
	}
	_, fErr = rFeature.UpdateData(true, model.FunctionTypeHvacSystemFunctionOperationModeRelationListData, relationData, nil, nil)
	assert.Nil(s.T(), fErr)

	functionData := &model.HvacSystemFunctionListDataType{
		HvacSystemFunctionData: []model.HvacSystemFunctionDataType{
			{
				SystemFunctionId:            util.Ptr(model.HvacSystemFunctionIdType(1)),
				CurrentOperationModeId:      util.Ptr(model.HvacOperationModeIdType(3)),
				IsOperationModeIdChangeable: isChangeable,
			},
		},
	}
	_, fErr = rFeature.UpdateData(true, model.FunctionTypeHvacSystemFunctionListData, functionData, nil, nil)
	assert.Nil(s.T(), fErr)
}

func (s *CaCDSFSuite) setSupportedScenarios(scenarios ...model.UseCaseScenarioSupportType) {
	remoteFeature := s.remoteDevice.FeatureByEntityTypeAndRole(
		s.dhwCircuitEntity, model.FeatureTypeTypeHvac, model.RoleTypeServer,
	)
	nodeAddress := &model.FeatureAddressType{
		Device:  s.remoteDevice.Address(),
		Entity:  []model.AddressEntityType{0},
		Feature: util.Ptr(model.AddressFeatureType(0)),
	}
	nodeFeature := s.remoteDevice.FeatureByAddress(nodeAddress)
	data := &model.NodeManagementUseCaseDataType{}
	data.AddUseCaseSupport(
		*remoteFeature.Address(),
		model.UseCaseActorTypeDHWCircuit,
		model.UseCaseNameTypeConfigurationOfDhwSystemFunction,
		"1.0.0",
		"release",
		true,
		scenarios,
	)
	_, err := nodeFeature.UpdateData(true, model.FunctionTypeNodeManagementUseCaseData, data, nil, nil)
	assert.Nil(s.T(), err)
	s.sut.UseCaseBase.HandleEvent(spineapi.EventPayload{
		Device:     s.remoteDevice,
		Entity:     s.dhwCircuitEntity,
		EventType:  spineapi.EventTypeDataChange,
		ChangeType: spineapi.ElementChangeUpdate,
		Data:       data,
	})
}
