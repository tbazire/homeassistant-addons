package internal

import (
	"github.com/enbility/eebus-go/features/server"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/spine"
	"github.com/enbility/spine-go/util"
	"github.com/stretchr/testify/assert"
)

func (s *InternalSuite) Test_GetElectricalConnectionId() {
	// Testing nil localEntity should return 0
	electricalConnectionId := GetElectricalConnectionId(nil)
	assert.Equal(s.T(), model.ElectricalConnectionIdType(0), electricalConnectionId)

	// Testing localEntity without electrical connection server feature should return 0
	localDevice := s.service.LocalDevice()
	testEntity := localDevice.EntityForType(model.EntityTypeTypeCEM)

	electricalConnectionId = GetElectricalConnectionId(testEntity)
	assert.Equal(s.T(), model.ElectricalConnectionIdType(0), electricalConnectionId)

	// Testing Create a test entity with electrical connection server feature
	// Add an electrical connection server feature
	electricalConnectionServerFeature := spine.NewFeatureLocal(20, testEntity, model.FeatureTypeTypeElectricalConnection, model.RoleTypeServer)
	electricalConnectionServerFeature.AddFunctionType(model.FunctionTypeElectricalConnectionDescriptionListData, true, false)
	electricalConnectionServerFeature.AddFunctionType(model.FunctionTypeElectricalConnectionParameterDescriptionListData, true, false)
	testEntity.AddFeature(electricalConnectionServerFeature)

	// Initially, there should be no electrical connection descriptions, so it should return 0
	electricalConnectionId = GetElectricalConnectionId(testEntity)
	assert.Equal(s.T(), model.ElectricalConnectionIdType(0), electricalConnectionId)

	// Testing Add electrical connection descriptions that don't match the filter
	// We need to create a server.ElectricalConnection instance to add descriptions
	electricalConnectionServer, err := server.NewElectricalConnection(testEntity)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), electricalConnectionServer)

	descDataNoMatch := model.ElectricalConnectionDescriptionDataType{
		ElectricalConnectionId:  util.Ptr(model.ElectricalConnectionIdType(100)),
		PowerSupplyType:         util.Ptr(model.ElectricalConnectionVoltageTypeTypeDc), // Different type (DC instead of AC)
		PositiveEnergyDirection: util.Ptr(model.EnergyDirectionTypeConsume),
	}

	err = electricalConnectionServer.AddDescription(descDataNoMatch)
	assert.Nil(s.T(), err)

	// Should still return 0 as no matching descriptions
	electricalConnectionId = GetElectricalConnectionId(testEntity)
	assert.Equal(s.T(), model.ElectricalConnectionIdType(0), electricalConnectionId)

	// Testing Add a matching electrical connection description
	descData := model.ElectricalConnectionDescriptionDataType{
		ElectricalConnectionId:  util.Ptr(model.ElectricalConnectionIdType(200)),
		PowerSupplyType:         util.Ptr(model.ElectricalConnectionVoltageTypeTypeAc),
		PositiveEnergyDirection: util.Ptr(model.EnergyDirectionTypeConsume),
	}

	err = electricalConnectionServer.AddDescription(descData)
	assert.Nil(s.T(), err)

	// Now it should return the correct electrical connection ID
	electricalConnectionId = GetElectricalConnectionId(testEntity)
	assert.Equal(s.T(), model.ElectricalConnectionIdType(200), electricalConnectionId)
}

func (s *InternalSuite) Test_GetParameterIdForACPowerTotalMeasurement() {
	// Testing nil localEntity should return 0
	parameterId := GetParameterIdForACPowerTotalMeasurement(nil, model.ElectricalConnectionIdType(1), model.MeasurementIdType(1))
	assert.Equal(s.T(), model.ElectricalConnectionParameterIdType(0), parameterId)

	// Testing localEntity without electrical connection server feature should return 0
	localDevice := s.service.LocalDevice()
	testEntity := localDevice.EntityForType(model.EntityTypeTypeCEM)

	parameterId = GetParameterIdForACPowerTotalMeasurement(testEntity, model.ElectricalConnectionIdType(1), model.MeasurementIdType(1))
	assert.Equal(s.T(), model.ElectricalConnectionParameterIdType(0), parameterId)

	// Testing Create a test entity with electrical connection server feature
	// Add an electrical connection server feature
	electricalConnectionServerFeature := spine.NewFeatureLocal(21, testEntity, model.FeatureTypeTypeElectricalConnection, model.RoleTypeServer)
	electricalConnectionServerFeature.AddFunctionType(model.FunctionTypeElectricalConnectionDescriptionListData, true, false)
	electricalConnectionServerFeature.AddFunctionType(model.FunctionTypeElectricalConnectionParameterDescriptionListData, true, false)
	testEntity.AddFeature(electricalConnectionServerFeature)

	// Initially, there should be no parameter descriptions, so it should return 0
	parameterId = GetParameterIdForACPowerTotalMeasurement(testEntity, model.ElectricalConnectionIdType(1), model.MeasurementIdType(1))
	assert.Equal(s.T(), model.ElectricalConnectionParameterIdType(0), parameterId)

	// Add parameter descriptions
	electricalConnectionServer, err := server.NewElectricalConnection(testEntity)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), electricalConnectionServer)

	// Testing Add parameter descriptions that don't match the filter
	paramDataNoMatch := model.ElectricalConnectionParameterDescriptionDataType{
		ElectricalConnectionId: util.Ptr(model.ElectricalConnectionIdType(1)),
		MeasurementId:          util.Ptr(model.MeasurementIdType(2)), // Different measurement ID
		VoltageType:            util.Ptr(model.ElectricalConnectionVoltageTypeTypeAc),
		AcMeasurementType:      util.Ptr(model.ElectricalConnectionAcMeasurementTypeTypeReal),
	}

	paramId1 := electricalConnectionServer.AddParameterDescription(paramDataNoMatch)
	assert.NotNil(s.T(), paramId1)

	// Should still return 0 as no matching descriptions
	parameterId = GetParameterIdForACPowerTotalMeasurement(testEntity, model.ElectricalConnectionIdType(1), model.MeasurementIdType(1))
	assert.Equal(s.T(), model.ElectricalConnectionParameterIdType(0), parameterId)

	// Testing Add a matching parameter description
	paramData := model.ElectricalConnectionParameterDescriptionDataType{
		ElectricalConnectionId: util.Ptr(model.ElectricalConnectionIdType(1)),
		MeasurementId:          util.Ptr(model.MeasurementIdType(1)),
		VoltageType:            util.Ptr(model.ElectricalConnectionVoltageTypeTypeAc),
		AcMeasurementType:      util.Ptr(model.ElectricalConnectionAcMeasurementTypeTypeReal),
	}

	paramId4 := electricalConnectionServer.AddParameterDescription(paramData)
	assert.NotNil(s.T(), paramId4)

	// Now it should return the correct parameter ID
	parameterId = GetParameterIdForACPowerTotalMeasurement(testEntity, model.ElectricalConnectionIdType(1), model.MeasurementIdType(1))
	assert.Equal(s.T(), *paramId4, parameterId)
}
