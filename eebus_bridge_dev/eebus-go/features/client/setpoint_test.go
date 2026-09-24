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

func TestSetpointSuite(t *testing.T) {
	suite.Run(t, new(SetpointSuite))
}

type SetpointSuite struct {
	suite.Suite

	localEntity  spineapi.EntityLocalInterface
	remoteEntity spineapi.EntityRemoteInterface

	setpoint    *Setpoint
	sentMessage []byte
}

var _ shipapi.ShipConnectionDataWriterInterface = (*SetpointSuite)(nil)

func (s *SetpointSuite) WriteShipMessageWithPayload(message []byte) {
	s.sentMessage = append(s.sentMessage[:0], message...)
}

func (s *SetpointSuite) BeforeTest(suiteName, testName string) {
	s.localEntity, s.remoteEntity = setupFeatures(
		s.T(),
		s,
		[]featureFunctions{
			{
				featureType: model.FeatureTypeTypeSetpoint,
				functions: []model.FunctionType{
					model.FunctionTypeSetpointDescriptionListData,
					model.FunctionTypeSetpointConstraintsListData,
					model.FunctionTypeSetpointListData,
				},
			},
		},
	)

	var err error
	s.setpoint, err = NewSetpoint(s.localEntity, nil)
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), s.setpoint)

	s.setpoint, err = NewSetpoint(s.localEntity, s.remoteEntity)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), s.setpoint)
}

func (s *SetpointSuite) Test_RequestSetpointDescriptions() {
	counter, err := s.setpoint.RequestSetpointDescriptions(nil, nil)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), counter)
}

func (s *SetpointSuite) Test_RequestSetpointConstraints() {
	counter, err := s.setpoint.RequestSetpointConstraints(nil, nil)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), counter)
}

func (s *SetpointSuite) Test_RequestSetpoints() {
	counter, err := s.setpoint.RequestSetpoints(nil, nil)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), counter)
}

func (s *SetpointSuite) Test_WriteSetpointListData() {
	counter, err := s.setpoint.WriteSetpointListData(nil)
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), counter)

	data := []model.SetpointDataType{}
	counter, err = s.setpoint.WriteSetpointListData(data)
	assert.NotNil(s.T(), err)
	assert.Nil(s.T(), counter)

	data = []model.SetpointDataType{
		{
			SetpointId: util.Ptr(model.SetpointIdType(1)),
			Value:      model.NewScaledNumberType(21),
		},
	}
	counter, err = s.setpoint.WriteSetpointListData(data)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), counter)
}

func (s *SetpointSuite) Test_WriteSetpointListData_PreservesCachedEntries() {
	remote := s.remoteEntity.FeatureOfTypeAndRole(model.FeatureTypeTypeSetpoint, model.RoleTypeServer)
	cached := &model.SetpointListDataType{SetpointData: []model.SetpointDataType{
		{SetpointId: util.Ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(20)},
		{SetpointId: util.Ptr(model.SetpointIdType(2)), Value: model.NewScaledNumberType(23)},
	}}
	_, updateErr := remote.UpdateData(true, model.FunctionTypeSetpointListData, cached, nil, nil)
	assert.Nil(s.T(), updateErr)

	_, err := s.setpoint.WriteSetpointListData([]model.SetpointDataType{{
		SetpointId: util.Ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(21),
	}})
	assert.NoError(s.T(), err)

	cmd := commandFromMessage(s.T(), s.sentMessage)
	assert.Empty(s.T(), cmd.Filter)
	assert.NotNil(s.T(), cmd.SetpointListData)
	assert.Len(s.T(), cmd.SetpointListData.SetpointData, 2)
	assert.Equal(s.T(), 21.0, cmd.SetpointListData.SetpointData[0].Value.GetValue())
	assert.Equal(s.T(), 23.0, cmd.SetpointListData.SetpointData[1].Value.GetValue())
}

func (s *SetpointSuite) Test_WriteSetpointListData_PartialPayload() {
	localEntity, remoteEntity := setupFeatures(
		s.T(),
		s,
		[]featureFunctions{{
			featureType: model.FeatureTypeTypeSetpoint,
			functions:   []model.FunctionType{model.FunctionTypeSetpointListData},
			partial:     true,
		}},
	)
	setpoint, err := NewSetpoint(localEntity, remoteEntity)
	assert.NoError(s.T(), err)

	_, err = setpoint.WriteSetpointListData([]model.SetpointDataType{{
		SetpointId: util.Ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(21),
	}})
	assert.NoError(s.T(), err)

	cmd := commandFromMessage(s.T(), s.sentMessage)
	assert.Len(s.T(), cmd.Filter, 1)
	assert.NotNil(s.T(), cmd.Filter[0].CmdControl.Partial)
	assert.Equal(s.T(), model.FunctionTypeSetpointListData, *cmd.Function)
	assert.Len(s.T(), cmd.SetpointListData.SetpointData, 1)
}
