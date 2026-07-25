package internal

import (
	"github.com/enbility/eebus-go/features/server"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
)

// GetElectricalConnectionId returns the ElectricalConnectionId for the AC Consume electrical connection
func GetElectricalConnectionId(localEntity spineapi.EntityLocalInterface) model.ElectricalConnectionIdType {
	if localEntity == nil {
		return model.ElectricalConnectionIdType(0)
	}
	electricalConnectionFeat := localEntity.FeatureOfTypeAndRole(model.FeatureTypeTypeElectricalConnection, model.RoleTypeServer)
	if electricalConnectionFeat == nil {
		return model.ElectricalConnectionIdType(0)
	}
	electricalConnection, err := server.NewElectricalConnection(localEntity)
	if err != nil || electricalConnection == nil {
		return model.ElectricalConnectionIdType(0)
	}
	electricalConnectionDescriptions, err := electricalConnection.GetDescriptionsForFilter(model.ElectricalConnectionDescriptionDataType{
		PowerSupplyType:         util.Ptr(model.ElectricalConnectionVoltageTypeTypeAc),
		PositiveEnergyDirection: util.Ptr(model.EnergyDirectionTypeConsume),
	})
	if err != nil || len(electricalConnectionDescriptions) != 1 || electricalConnectionDescriptions[0].ElectricalConnectionId == nil {
		return model.ElectricalConnectionIdType(0)
	}
	return *electricalConnectionDescriptions[0].ElectricalConnectionId
}

// GetParameterIdForACPowerTotalMeasurement returns the ParameterId for the AC Power Total measurement on the specified electrical connection
func GetParameterIdForACPowerTotalMeasurement(localEntity spineapi.EntityLocalInterface,
	electricalConnectionId model.ElectricalConnectionIdType,
	measurementId model.MeasurementIdType) model.ElectricalConnectionParameterIdType {
	if localEntity == nil {
		return model.ElectricalConnectionParameterIdType(0)
	}
	electricalConnectionFeat := localEntity.FeatureOfTypeAndRole(model.FeatureTypeTypeElectricalConnection, model.RoleTypeServer)
	if electricalConnectionFeat == nil {
		return model.ElectricalConnectionParameterIdType(0)
	}
	electricalConnection, err := server.NewElectricalConnection(localEntity)
	if err != nil || electricalConnection == nil {
		return model.ElectricalConnectionParameterIdType(0)
	}
	parameterDescriptions, err := electricalConnection.GetParameterDescriptionsForFilter(model.ElectricalConnectionParameterDescriptionDataType{
		ElectricalConnectionId: util.Ptr(electricalConnectionId),
		MeasurementId:          util.Ptr(measurementId),
		VoltageType:            util.Ptr(model.ElectricalConnectionVoltageTypeTypeAc),
		AcMeasurementType:      util.Ptr(model.ElectricalConnectionAcMeasurementTypeTypeReal),
	})
	if err != nil || len(parameterDescriptions) != 1 || parameterDescriptions[0].ParameterId == nil {
		return model.ElectricalConnectionParameterIdType(0)
	}
	return *parameterDescriptions[0].ParameterId
}
