package scanner

import (
	"fmt"
	"strings"

	"github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/service"
	"github.com/enbility/eebus-go/usecases/cem/vabd"
	"github.com/enbility/eebus-go/usecases/cem/vapd"
	"github.com/enbility/eebus-go/usecases/ma/mgcp"
	"github.com/enbility/eebus-go/usecases/ma/mpc"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// UseCases bundles the typed (semantic) use cases the scanner registers on the
// local CEM entity. Each use case filters the remote data by its expected
// scope/role and produces nicely labeled output.
//
// These are read-only use cases — no writes/limits are sent in V1.
type UseCases struct {
	mgcp ucMGCP
	mpc  ucMPC
	vabd ucVABD
	vapd ucVAPD
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
)

// RegisterUseCases wires the read-only use cases into the service, on the given
// local entity. The returned *UseCases can be retained to trigger manual reads.
//
// Use cases whose remote entity type does not match simply remain inactive —
// they do not produce errors. This is why we register all of them upfront.
func RegisterUseCases(svc *service.Service, localEntity spineapi.EntityLocalInterface) (*UseCases, error) {
	uc := &UseCases{}

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

	return uc, nil
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
