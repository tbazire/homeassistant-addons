package api

import (
	"github.com/enbility/eebus-go/api"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// Actor: Configuration Appliance
// UseCase: Configuration of Room Heating Temperature
type CaCRHTInterface interface {
	api.UseCaseInterface

	// Scenario 1

	// State returns the complete room-air temperature setpoint selected for
	// room heating. Duplicate relation references to the same setpoint are
	// deduplicated. Missing fields or multiple distinct candidates fail closed.
	State(entity spineapi.EntityRemoteInterface) (RoomHeatingSetpointState, error)

	// return the current room heating temperature setpoints
	//
	// parameters:
	//   - entity: the entity of the HVAC room
	//
	// possible errors:
	//   - ErrDataNotAvailable if no such data is (yet) available
	//   - and others
	Setpoints(entity spineapi.EntityRemoteInterface) ([]Setpoint, error)

	// return the constraints for the room heating temperature setpoints
	//
	// parameters:
	//   - entity: the entity of the HVAC room
	//
	// possible errors:
	//   - ErrDataNotAvailable if no such data is (yet) available
	//   - and others
	SetpointConstraints(entity spineapi.EntityRemoteInterface) ([]SetpointConstraints, error)

	// write the room heating temperature setpoint for a heating operation mode
	//
	// parameters:
	//   - entity: the entity of the HVAC room
	//   - mode: the heating operation mode the setpoint is used for (on, off or eco)
	//   - degC: the temperature setpoint in degree Celsius
	//
	// possible errors:
	//   - ErrNotSupported if the setpoint is not changeable or the mode is auto
	//   - ErrDataNotAvailable if the required data is not (yet) available
	//   - and others
	WriteSetpoint(
		entity spineapi.EntityRemoteInterface,
		mode HvacOperationModeType,
		degC float64,
		resultCB func(result model.ResultDataType, msgCounter model.MsgCounterType),
	) (*model.MsgCounterType, error)

	// WriteRoomAirTemperatureSetpoint writes the single relation-safe room-air
	// temperature setpoint selected by State, independent of operation mode.
	// Accepted device results trigger a setpoint refresh before resultCB runs.
	WriteRoomAirTemperatureSetpoint(
		entity spineapi.EntityRemoteInterface,
		degC float64,
		resultCB func(result model.ResultDataType, msgCounter model.MsgCounterType),
	) (*model.MsgCounterType, error)
}
