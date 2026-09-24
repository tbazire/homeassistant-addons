package scanner

import (
	"fmt"
	"strings"

	"github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/service"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	"github.com/enbility/eebus-go/usecases/cem/vabd"
	"github.com/enbility/eebus-go/usecases/cem/vapd"
	"github.com/enbility/eebus-go/usecases/ma/mdsf"
	"github.com/enbility/eebus-go/usecases/ma/mdt"
	"github.com/enbility/eebus-go/usecases/ma/mgcp"
	"github.com/enbility/eebus-go/usecases/ma/mot"
	"github.com/enbility/eebus-go/usecases/ma/mpc"
	"github.com/enbility/eebus-go/usecases/ma/mrhsf"
	"github.com/enbility/eebus-go/usecases/ma/mrt"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// SignalSink forwards one semantic read value from a typed use case toward the
// bridge. The daemon implements it by emitting a "uc_signal" NDJSON line (see
// App.emitSignal), so HVAC temperatures and operation modes surface as Home
// Assistant sensors attached to the same device as the control entities. An
// empty value means "not available" and is skipped by the caller.
type SignalSink func(ski string, entity spineapi.EntityRemoteInterface, usecase, signal, value, valueType, unit string)

// RegisterOptions tunes RegisterUseCases. Zero value keeps the legacy behavior
// (the four base read use cases, signal sink disabled).
type RegisterOptions struct {
	// HVAC adds the five HVAC read use cases (MDT, MDSF, MOT, MRT, MRHSF) to
	// the registration. Gated by the -write-hvac-enabled flag: without it, no
	// HVAC entity is announced or emitted, and the generic scanner remains the
	// only surface for raw temperature measurements.
	HVAC bool

	// Signal is the sink semantic read values are forwarded to. May be nil
	// (text mode / tests): values are then logged instead, matching the
	// historical behavior of the base use cases.
	Signal SignalSink
}

// UseCases bundles the typed (semantic) use cases the scanner registers on the
// local CEM entity. Each use case filters the remote data by its expected
// scope/role and produces nicely labeled output.
//
// The base set (MGCP, MPC, VABD, VAPD) is read-only and always registered. The
// HVAC set (MDT, MDSF, MOT, MRT, MRHSF) is opt-in via RegisterOptions.HVAC and
// forwards its values through the Signal sink so the bridge can expose them.
type UseCases struct {
	mgcp ucMGCP
	mpc  ucMPC
	vabd ucVABD
	vapd ucVAPD

	// HVAC read use cases (nil when HVAC is disabled).
	mdt   ucMDT
	mdsf  ucMDSF
	mot   ucMOT
	mrt   ucMRT
	mrhsf ucMRHSF

	signal SignalSink
}

// The concrete use case implementations, kept as fields so the event callbacks
// can call back into them to read the freshly updated values.
type (
	ucMGCP interface {
		Power(entity spineapi.EntityRemoteInterface) (float64, error)
		EnergyFeedIn(entity spineapi.EntityRemoteInterface) (float64, error)
		EnergyConsumed(entity spineapi.EntityRemoteInterface) (float64, error)
		CurrentPerPhase(entity spineapi.EntityRemoteInterface) ([]float64, error)
		VoltagePerPhase(entity spineapi.EntityRemoteInterface) ([]float64, error)
		Frequency(entity spineapi.EntityRemoteInterface) (float64, error)
		PowerLimitationFactor(entity spineapi.EntityRemoteInterface) (float64, error)
	}
	ucMPC interface {
		Power(entity spineapi.EntityRemoteInterface) (float64, error)
		PowerPerPhase(entity spineapi.EntityRemoteInterface) ([]float64, error)
		EnergyConsumed(entity spineapi.EntityRemoteInterface) (float64, error)
		EnergyProduced(entity spineapi.EntityRemoteInterface) (float64, error)
		CurrentPerPhase(entity spineapi.EntityRemoteInterface) ([]float64, error)
		VoltagePerPhase(entity spineapi.EntityRemoteInterface) ([]float64, error)
		Frequency(entity spineapi.EntityRemoteInterface) (float64, error)
	}
	ucVABD interface {
		Power(entity spineapi.EntityRemoteInterface) (float64, error)
		EnergyCharged(entity spineapi.EntityRemoteInterface) (float64, error)
		EnergyDischarged(entity spineapi.EntityRemoteInterface) (float64, error)
		StateOfCharge(entity spineapi.EntityRemoteInterface) (float64, error)
	}
	ucVAPD interface {
		Power(entity spineapi.EntityRemoteInterface) (float64, error)
		PowerNominalPeak(entity spineapi.EntityRemoteInterface) (float64, error)
		PVYieldTotal(entity spineapi.EntityRemoteInterface) (float64, error)
	}
	// HVAC read use cases. The interfaces are local subsets of the upstream
	// Ma*Interface contracts, narrowed to what the bridge consumes; the concrete
	// types are the grafted eebus-go implementations.
	ucMDT interface {
		Temperature(entity spineapi.EntityRemoteInterface, unit model.UnitOfMeasurementType) (float64, error)
	}
	ucMDSF interface {
		OperationModes(entity spineapi.EntityRemoteInterface) ([]ucapi.HvacOperationModeType, error)
		CurrentOperationMode(entity spineapi.EntityRemoteInterface) (ucapi.HvacOperationModeType, error)
		IsOverrunActive(entity spineapi.EntityRemoteInterface) (bool, error)
		OverrunStatus(entity spineapi.EntityRemoteInterface) (model.HvacOverrunStatusType, error)
	}
	ucMOT interface {
		Temperature(entity spineapi.EntityRemoteInterface, unit model.UnitOfMeasurementType) (float64, error)
	}
	ucMRT interface {
		Temperature(entity spineapi.EntityRemoteInterface, unit model.UnitOfMeasurementType) (float64, error)
	}
	ucMRHSF interface {
		OperationModes(entity spineapi.EntityRemoteInterface) ([]ucapi.HvacOperationModeType, error)
		CurrentOperationMode(entity spineapi.EntityRemoteInterface) (ucapi.HvacOperationModeType, error)
	}
)

// RegisterUseCases wires the read-only use cases into the service, on the given
// local entity. The returned *UseCases can be retained to trigger manual reads.
//
// Use cases whose remote entity type does not match simply remain inactive —
// they do not produce errors. This is why we register all of them upfront.
func RegisterUseCases(svc *service.Service, localEntity spineapi.EntityLocalInterface, opts RegisterOptions) (*UseCases, error) {
	uc := &UseCases{signal: opts.Signal}

	// MA MGCP — Monitoring of Grid Connection Point.
	m := mgcp.NewMGCP(localEntity, uc.onMGCPEvent)
	if err := svc.AddUseCase(m); err != nil {
		return nil, fmt.Errorf("add usecase mgcp: %w", err)
	}
	uc.mgcp = m

	// MA MPC — Monitoring of Power Consumption (appliances).
	p := mpc.NewMPC(localEntity, uc.onMPCEvent)
	if err := svc.AddUseCase(p); err != nil {
		return nil, fmt.Errorf("add usecase mpc: %w", err)
	}
	uc.mpc = p

	// CEM VABD — Visualization of Aggregated Battery Data.
	b := vabd.NewVABD(localEntity, uc.onVABDEvent)
	if err := svc.AddUseCase(b); err != nil {
		return nil, fmt.Errorf("add usecase vabd: %w", err)
	}
	uc.vabd = b

	// CEM VAPD — Visualization of Aggregated Photovoltaic Data.
	v := vapd.NewVAPD(localEntity, uc.onVAPDEvent)
	if err := svc.AddUseCase(v); err != nil {
		return nil, fmt.Errorf("add usecase vapd: %w", err)
	}
	uc.vapd = v

	if opts.HVAC {
		if err := uc.registerHVAC(svc, localEntity); err != nil {
			return nil, err
		}
	}

	return uc, nil
}

// registerHVAC adds the five HVAC read use cases. Each targets a specific
// remote entity type (DHW circuit, HVAC room, temperature sensor) and stays
// inert against any remote that does not announce it via UseCaseSupportData —
// which is what guarantees "no entity change for a non-HVAC remote".
func (uc *UseCases) registerHVAC(svc *service.Service, localEntity spineapi.EntityLocalInterface) error {
	add := func(name string, u api.UseCaseInterface) error {
		if err := svc.AddUseCase(u); err != nil {
			return fmt.Errorf("add usecase %s: %w", name, err)
		}
		return nil
	}

	// MA MDT — Monitoring of DHW Temperature (DHWCircuit entities).
	t := mdt.NewMDT(localEntity, uc.onMDTEvent)
	if err := add("mdt", t); err != nil {
		return err
	}
	uc.mdt = t

	// MA MDSF — Monitoring of DHW System Function (DHWCircuit entities).
	ds := mdsf.NewMDSF(localEntity, uc.onMDSFEvent)
	if err := add("mdsf", ds); err != nil {
		return err
	}
	uc.mdsf = ds

	// MA MOT — Monitoring of Outdoor Temperature (TemperatureSensor entities).
	o := mot.NewMOT(localEntity, uc.onMOTEvent)
	if err := add("mot", o); err != nil {
		return err
	}
	uc.mot = o

	// MA MRT — Monitoring of Room Temperature (HVACRoom entities).
	r := mrt.NewMRT(localEntity, uc.onMRTEvent)
	if err := add("mrt", r); err != nil {
		return err
	}
	uc.mrt = r

	// MA MRHSF — Monitoring of Room Heating System Function (HVACRoom entities).
	rs := mrhsf.NewMRHSF(localEntity, uc.onMRHSFEvent)
	if err := add("mrhsf", rs); err != nil {
		return err
	}
	uc.mrhsf = rs

	return nil
}

// ---- HVAC read use cases ----------------------------------------------------

// onMDTEvent forwards the DHW temperature as the "mdt/temperature" signal —
// the value the bridge wires into the water_heater's current_temperature.
func (uc *UseCases) onMDTEvent(ski string, _ spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
	if event != mdt.DataUpdateTemperature {
		return
	}
	uc.emitTemperature("MDT", "mdt", ski, entity, func() (float64, error) {
		return uc.mdt.Temperature(entity, model.UnitOfMeasurementTypedegC)
	})
}

// onMOTEvent forwards the outdoor temperature as the "mot/temperature" signal.
func (uc *UseCases) onMOTEvent(ski string, _ spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
	if event != mot.DataUpdateTemperature {
		return
	}
	uc.emitTemperature("MOT", "mot", ski, entity, func() (float64, error) {
		return uc.mot.Temperature(entity, model.UnitOfMeasurementTypedegC)
	})
}

// onMRTEvent forwards the HVAC room temperature as the "mrt/temperature" signal.
func (uc *UseCases) onMRTEvent(ski string, _ spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
	if event != mrt.DataUpdateTemperature {
		return
	}
	uc.emitTemperature("MRT", "mrt", ski, entity, func() (float64, error) {
		return uc.mrt.Temperature(entity, model.UnitOfMeasurementTypedegC)
	})
}

// onMDSFEvent forwards the DHW operation mode and the one-time DHW overrun
// state ("mdsf/operation_mode", "mdsf/overrun_active", "mdsf/overrun_status").
// The operation mode feeds the water_heater's mode state.
func (uc *UseCases) onMDSFEvent(ski string, _ spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
	switch event {
	case mdsf.DataUpdateOperationMode:
		if mode, err := uc.mdsf.CurrentOperationMode(entity); err == nil {
			uc.emitSignal(ski, entity, "mdsf", "operation_mode", string(mode), "string", "")
		} else {
			logDebugf("[MDSF] operation mode ski=%s entity=%s: %v", ski, entityLabel(entity), err)
		}
	case mdsf.DataUpdateOverrun:
		if active, err := uc.mdsf.IsOverrunActive(entity); err == nil {
			uc.emitSignal(ski, entity, "mdsf", "overrun_active", boolString(active), "boolean", "")
		}
		if status, err := uc.mdsf.OverrunStatus(entity); err == nil {
			uc.emitSignal(ski, entity, "mdsf", "overrun_status", string(status), "string", "")
		}
	}
}

// onMRHSFEvent forwards the room heating operation mode ("mrhsf/operation_mode").
func (uc *UseCases) onMRHSFEvent(ski string, _ spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
	if event != mrhsf.DataUpdateOperationMode {
		return
	}
	if mode, err := uc.mrhsf.CurrentOperationMode(entity); err == nil {
		uc.emitSignal(ski, entity, "mrhsf", "operation_mode", string(mode), "string", "")
	} else {
		logDebugf("[MRHSF] operation mode ski=%s entity=%s: %v", ski, entityLabel(entity), err)
	}
}

// emitTemperature reads one temperature in °C and forwards it as a number
// signal; in text mode (no sink) it logs, like the base use cases.
func (uc *UseCases) emitTemperature(label, usecase, ski string, entity spineapi.EntityRemoteInterface, get func() (float64, error)) {
	v, err := get()
	if err != nil {
		logDebugf("[%s] temperature ski=%s entity=%s: %v", label, ski, entityLabel(entity), err)
		return
	}
	uc.emitSignal(ski, entity, usecase, "temperature", formatTemp(v), "number", "°C")
}

// emitSignal forwards one value to the sink when wired, else logs it. An empty
// value is never forwarded.
func (uc *UseCases) emitSignal(ski string, entity spineapi.EntityRemoteInterface, usecase, signal, value, valueType, unit string) {
	if value == "" {
		return
	}
	if uc.signal != nil {
		uc.signal(ski, entity, usecase, signal, value, valueType, unit)
		return
	}
	logInfof("[%s] %s = %s (ski=%s entity=%s)", usecase, signal, value, ski, entityLabel(entity))
}

// formatTemp renders a temperature compactly (integers without decimals).
func formatTemp(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	s := fmt.Sprintf("%.1f", v)
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}

// boolString renders a boolean the way the bridge's uc_signal contract expects.
func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// ---- MGCP (grid connection point) -----------------------------------------

func (uc *UseCases) onMGCPEvent(ski string, _ spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
	switch event {
	case mgcp.DataUpdatePower:
		printScalar("MGCP", ski, entity, "Power", func() (float64, error) { return uc.mgcp.Power(entity) }, "W")
	case mgcp.DataUpdateEnergyFeedIn:
		printScalar("MGCP", ski, entity, "EnergyFeedIn", func() (float64, error) { return uc.mgcp.EnergyFeedIn(entity) }, "Wh")
	case mgcp.DataUpdateEnergyConsumed:
		printScalar("MGCP", ski, entity, "EnergyConsumed", func() (float64, error) { return uc.mgcp.EnergyConsumed(entity) }, "Wh")
	case mgcp.DataUpdateCurrentPerPhase:
		printPhases("MGCP", ski, entity, "CurrentPerPhase", func() ([]float64, error) { return uc.mgcp.CurrentPerPhase(entity) }, "A")
	case mgcp.DataUpdateVoltagePerPhase:
		printPhases("MGCP", ski, entity, "VoltagePerPhase", func() ([]float64, error) { return uc.mgcp.VoltagePerPhase(entity) }, "V")
	case mgcp.DataUpdateFrequency:
		printScalar("MGCP", ski, entity, "Frequency", func() (float64, error) { return uc.mgcp.Frequency(entity) }, "Hz")
	case mgcp.DataUpdatePowerLimitationFactor:
		printScalar("MGCP", ski, entity, "PowerLimitationFactor", func() (float64, error) { return uc.mgcp.PowerLimitationFactor(entity) }, "")
	}
}

// ---- MPC (appliance power consumption) ------------------------------------

func (uc *UseCases) onMPCEvent(ski string, _ spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
	switch event {
	case mpc.DataUpdatePower:
		printScalar("MPC", ski, entity, "Power", func() (float64, error) { return uc.mpc.Power(entity) }, "W")
	case mpc.DataUpdatePowerPerPhase:
		printPhases("MPC", ski, entity, "PowerPerPhase", func() ([]float64, error) { return uc.mpc.PowerPerPhase(entity) }, "W")
	case mpc.DataUpdateEnergyConsumed:
		printScalar("MPC", ski, entity, "EnergyConsumed", func() (float64, error) { return uc.mpc.EnergyConsumed(entity) }, "Wh")
	case mpc.DataUpdateEnergyProduced:
		printScalar("MPC", ski, entity, "EnergyProduced", func() (float64, error) { return uc.mpc.EnergyProduced(entity) }, "Wh")
	case mpc.DataUpdateCurrentsPerPhase:
		printPhases("MPC", ski, entity, "CurrentPerPhase", func() ([]float64, error) { return uc.mpc.CurrentPerPhase(entity) }, "A")
	case mpc.DataUpdateVoltagePerPhase:
		printPhases("MPC", ski, entity, "VoltagePerPhase", func() ([]float64, error) { return uc.mpc.VoltagePerPhase(entity) }, "V")
	case mpc.DataUpdateFrequency:
		printScalar("MPC", ski, entity, "Frequency", func() (float64, error) { return uc.mpc.Frequency(entity) }, "Hz")
	}
}

// ---- VABD (battery) --------------------------------------------------------

func (uc *UseCases) onVABDEvent(ski string, _ spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
	switch event {
	case vabd.DataUpdatePower:
		printScalar("VABD", ski, entity, "Power", func() (float64, error) { return uc.vabd.Power(entity) }, "W")
	case vabd.DataUpdateEnergyCharged:
		printScalar("VABD", ski, entity, "EnergyCharged", func() (float64, error) { return uc.vabd.EnergyCharged(entity) }, "Wh")
	case vabd.DataUpdateEnergyDischarged:
		printScalar("VABD", ski, entity, "EnergyDischarged", func() (float64, error) { return uc.vabd.EnergyDischarged(entity) }, "Wh")
	case vabd.DataUpdateStateOfCharge:
		printScalar("VABD", ski, entity, "StateOfCharge", func() (float64, error) { return uc.vabd.StateOfCharge(entity) }, "%")
	}
}

// ---- VAPD (photovoltaic) --------------------------------------------------

func (uc *UseCases) onVAPDEvent(ski string, _ spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
	switch event {
	case vapd.DataUpdatePower:
		printScalar("VAPD", ski, entity, "Power", func() (float64, error) { return uc.vapd.Power(entity) }, "W")
	case vapd.DataUpdatePowerNominalPeak:
		printScalar("VAPD", ski, entity, "PowerNominalPeak", func() (float64, error) { return uc.vapd.PowerNominalPeak(entity) }, "W")
	case vapd.DataUpdatePVYieldTotal:
		printScalar("VAPD", ski, entity, "PVYieldTotal", func() (float64, error) { return uc.vapd.PVYieldTotal(entity) }, "Wh")
	}
}

// ---- Print helpers ---------------------------------------------------------

func entityLabel(entity spineapi.EntityRemoteInterface) string {
	if entity == nil {
		return "?"
	}
	if entity.Address() != nil {
		parts := make([]string, 0, len(entity.Address().Entity))
		for _, a := range entity.Address().Entity {
			parts = append(parts, fmt.Sprintf("%d", a))
		}
		return strings.Join(parts, ".")
	}
	return string(entity.EntityType())
}

func printScalar(uc, ski string, entity spineapi.EntityRemoteInterface, name string, get func() (float64, error), unit string) {
	v, err := get()
	if err != nil {
		// ErrDataNotAvailable is common right after binding, before the first
		// value has been received; keep it at debug level to avoid noise.
		logDebugf("[%s] %s ski=%s entity=%s: %v", uc, name, ski, entityLabel(entity), err)
		return
	}
	if unit != "" {
		logInfof("[%s] %s = %.6g %s  (ski=%s entity=%s)", uc, name, v, unit, ski, entityLabel(entity))
	} else {
		logInfof("[%s] %s = %.6g  (ski=%s entity=%s)", uc, name, v, ski, entityLabel(entity))
	}
}

func printPhases(uc, ski string, entity spineapi.EntityRemoteInterface, name string, get func() ([]float64, error), unit string) {
	v, err := get()
	if err != nil {
		logDebugf("[%s] %s ski=%s entity=%s: %v", uc, name, ski, entityLabel(entity), err)
		return
	}
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = fmt.Sprintf("%.6g", x)
	}
	logInfof("[%s] %s = [%s] %s  (ski=%s entity=%s)", uc, name, strings.Join(parts, ", "), unit, ski, entityLabel(entity))
}

// entityType models the local entity type used by the scanner (CEM).
// Kept here to centralize the constant used both by main and by RegisterUseCases.
const localEntityType = model.EntityTypeTypeCEM
