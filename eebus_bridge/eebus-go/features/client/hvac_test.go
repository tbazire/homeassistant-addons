package client

import (
	"testing"

	shipapi "github.com/enbility/ship-go/api"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestHvacSuite(t *testing.T) {
	suite.Run(t, new(HvacSuite))
}

type HvacSuite struct {
	suite.Suite

	localEntity  spineapi.EntityLocalInterface
	remoteEntity spineapi.EntityRemoteInterface

	hvac        *Hvac
	sentMessage []byte
}

var _ shipapi.ShipConnectionDataWriterInterface = (*HvacSuite)(nil)

func (s *HvacSuite) WriteShipMessageWithPayload(message []byte) {
	s.sentMessage = append(s.sentMessage[:0], message...)
}

func (s *HvacSuite) BeforeTest(suiteName, testName string) {
	s.localEntity, s.remoteEntity = setupFeatures(
		s.T(),
		s,
		[]featureFunctions{
			{
				featureType: model.FeatureTypeTypeHvac,
				functions: []model.FunctionType{
					model.FunctionTypeHvacSystemFunctionDescriptionListData,
					model.FunctionTypeHvacSystemFunctionListData,
					model.FunctionTypeHvacOperationModeDescriptionListData,
					model.FunctionTypeHvacSystemFunctionOperationModeRelationListData,
					model.FunctionTypeHvacSystemFunctionSetPointRelationListData,
					model.FunctionTypeHvacOverrunDescriptionListData,
					model.FunctionTypeHvacOverrunListData,
				},
			},
		},
	)

	var err error
	s.hvac, err = NewHvac(s.localEntity, nil)
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), s.hvac)

	s.hvac, err = NewHvac(s.localEntity, s.remoteEntity)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), s.hvac)
}

func (s *HvacSuite) Test_RequestHvacSystemFunctionDescriptions() {
	counter, err := s.hvac.RequestHvacSystemFunctionDescriptions(nil, nil)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), counter)
}

func (s *HvacSuite) Test_RequestHvacSystemFunctions() {
	counter, err := s.hvac.RequestHvacSystemFunctions(nil, nil)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), counter)
}

func (s *HvacSuite) Test_RequestHvacOperationModeDescriptions() {
	counter, err := s.hvac.RequestHvacOperationModeDescriptions(nil, nil)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), counter)
}

func (s *HvacSuite) Test_RequestHvacSystemFunctionOperationModeRelations() {
	counter, err := s.hvac.RequestHvacSystemFunctionOperationModeRelations(nil, nil)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), counter)
}

func (s *HvacSuite) Test_RequestHvacSystemFunctionSetpointRelations() {
	counter, err := s.hvac.RequestHvacSystemFunctionSetpointRelations(nil, nil)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), counter)
}

func (s *HvacSuite) Test_WriteHvacSystemFunctionListData() {
	counter, err := s.hvac.WriteHvacSystemFunctionListData(nil)
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), counter)

	data := []model.HvacSystemFunctionDataType{}
	counter, err = s.hvac.WriteHvacSystemFunctionListData(data)
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), counter)

	data = []model.HvacSystemFunctionDataType{
		{
			SystemFunctionId:       util.Ptr(model.HvacSystemFunctionIdType(1)),
			CurrentOperationModeId: util.Ptr(model.HvacOperationModeIdType(2)),
		},
	}
	counter, err = s.hvac.WriteHvacSystemFunctionListData(data)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), counter)
}

func (s *HvacSuite) Test_RequestHvacOverrunDescriptions() {
	counter, err := s.hvac.RequestHvacOverrunDescriptions(nil, nil)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), counter)
}

func (s *HvacSuite) Test_RequestHvacOverruns() {
	counter, err := s.hvac.RequestHvacOverruns(nil, nil)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), counter)
}

func (s *HvacSuite) Test_WriteHvacOverrunListData() {
	counter, err := s.hvac.WriteHvacOverrunListData(nil)
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), counter)

	data := []model.HvacOverrunDataType{
		{
			OverrunId:     util.Ptr(model.HvacOverrunIdType(1)),
			OverrunStatus: util.Ptr(model.HvacOverrunStatusTypeActive),
		},
	}
	counter, err = s.hvac.WriteHvacOverrunListData(data)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), counter)
}

func (s *HvacSuite) Test_WriteHvacOverrunListData_PreservesCachedEntries() {
	remote := s.remoteEntity.FeatureOfTypeAndRole(model.FeatureTypeTypeHvac, model.RoleTypeServer)
	cached := &model.HvacOverrunListDataType{HvacOverrunData: []model.HvacOverrunDataType{
		{OverrunId: util.Ptr(model.HvacOverrunIdType(1)), OverrunStatus: util.Ptr(model.HvacOverrunStatusTypeInactive)},
		{OverrunId: util.Ptr(model.HvacOverrunIdType(2)), OverrunStatus: util.Ptr(model.HvacOverrunStatusTypeFinished)},
	}}
	_, updateErr := remote.UpdateData(true, model.FunctionTypeHvacOverrunListData, cached, nil, nil)
	assert.Nil(s.T(), updateErr)

	_, err := s.hvac.WriteHvacOverrunListData([]model.HvacOverrunDataType{{
		OverrunId: util.Ptr(model.HvacOverrunIdType(1)), OverrunStatus: util.Ptr(model.HvacOverrunStatusTypeActive),
	}})
	assert.NoError(s.T(), err)

	cmd := commandFromMessage(s.T(), s.sentMessage)
	assert.Empty(s.T(), cmd.Filter)
	assert.NotNil(s.T(), cmd.HvacOverrunListData)
	assert.Len(s.T(), cmd.HvacOverrunListData.HvacOverrunData, 2)
	assert.Equal(s.T(), model.HvacOverrunStatusTypeActive, *cmd.HvacOverrunListData.HvacOverrunData[0].OverrunStatus)
	assert.Equal(s.T(), model.HvacOverrunStatusTypeFinished, *cmd.HvacOverrunListData.HvacOverrunData[1].OverrunStatus)
}

func (s *HvacSuite) Test_WriteHvacSystemFunctionListData_PreservesCachedEntries() {
	remote := s.remoteEntity.FeatureOfTypeAndRole(model.FeatureTypeTypeHvac, model.RoleTypeServer)
	cached := &model.HvacSystemFunctionListDataType{HvacSystemFunctionData: []model.HvacSystemFunctionDataType{
		{SystemFunctionId: util.Ptr(model.HvacSystemFunctionIdType(1)), CurrentOperationModeId: util.Ptr(model.HvacOperationModeIdType(1))},
		{SystemFunctionId: util.Ptr(model.HvacSystemFunctionIdType(2)), CurrentOperationModeId: util.Ptr(model.HvacOperationModeIdType(3))},
	}}
	_, updateErr := remote.UpdateData(true, model.FunctionTypeHvacSystemFunctionListData, cached, nil, nil)
	assert.Nil(s.T(), updateErr)

	_, err := s.hvac.WriteHvacSystemFunctionListData([]model.HvacSystemFunctionDataType{{
		SystemFunctionId: util.Ptr(model.HvacSystemFunctionIdType(1)), CurrentOperationModeId: util.Ptr(model.HvacOperationModeIdType(2)),
	}})
	assert.NoError(s.T(), err)

	cmd := commandFromMessage(s.T(), s.sentMessage)
	assert.Empty(s.T(), cmd.Filter)
	assert.NotNil(s.T(), cmd.HvacSystemFunctionListData)
	assert.Len(s.T(), cmd.HvacSystemFunctionListData.HvacSystemFunctionData, 2)
	assert.Equal(s.T(), model.HvacOperationModeIdType(2), *cmd.HvacSystemFunctionListData.HvacSystemFunctionData[0].CurrentOperationModeId)
	assert.Equal(s.T(), model.HvacOperationModeIdType(3), *cmd.HvacSystemFunctionListData.HvacSystemFunctionData[1].CurrentOperationModeId)
}

func (s *HvacSuite) Test_WriteHvacSystemFunctionListData_Partial() {
	localEntity, remoteEntity := setupFeatures(
		s.T(),
		s,
		[]featureFunctions{
			{
				featureType: model.FeatureTypeTypeHvac,
				functions: []model.FunctionType{
					model.FunctionTypeHvacSystemFunctionListData,
				},
				partial: true,
			},
		},
	)

	hvac, err := NewHvac(localEntity, remoteEntity)
	assert.Nil(s.T(), err)

	data := []model.HvacSystemFunctionDataType{
		{
			SystemFunctionId:       util.Ptr(model.HvacSystemFunctionIdType(1)),
			CurrentOperationModeId: util.Ptr(model.HvacOperationModeIdType(2)),
		},
	}
	counter, err := hvac.WriteHvacSystemFunctionListData(data)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), counter)

	cmd := commandFromMessage(s.T(), s.sentMessage)
	assert.Len(s.T(), cmd.Filter, 1)
	assert.NotNil(s.T(), cmd.Filter[0].CmdControl)
	assert.NotNil(s.T(), cmd.Filter[0].CmdControl.Partial)
	assert.Equal(s.T(), model.FunctionTypeHvacSystemFunctionListData, *cmd.Function)
	assert.Len(s.T(), cmd.HvacSystemFunctionListData.HvacSystemFunctionData, 1)
}
