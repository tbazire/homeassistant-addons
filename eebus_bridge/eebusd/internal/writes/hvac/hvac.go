// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package hvac implements the wucapi.WriteUseCase contract for the four EEBUS
// HVAC configuration use cases grafted from the volschin/eebus-go fork:
//
//	cdt   — Configuration of DHW Temperature        → number  (target temp)
//	cdsf  — Configuration of DHW System Function    → select  (operation mode)
//	crht  — Configuration of Room Heating Temp.     → number  (room setpoint)
//	crhsf — Configuration of Room Heating Sys.Func. → select  (operation mode)
//
// The matching READ use cases (mdt, mdsf, mot, mrt, mrhsf) live in the scanner
// package (see scanner.RegisterUseCases) and feed the semantic sensors. The
// write modules emit "controllable" lines; the bridge composes the DHWCircuit
// ones into a Home Assistant water_heater entity and renders the HVACRoom ones
// as climate-style controls (select/switch for the mode, number for the
// setpoint).
//
// Genericity: every module targets a remote ENTITY TYPE (DHWCircuit or
// HVACRoom) and activates only through the standard EEBUS negotiation — the
// remote must announce the use case via UseCaseSupportData and the scenarios
// must match (IsCompatible → AvailableScenariosForEntity). No brand, model or
// SKI appears anywhere. The whole set is opt-in behind -write-hvac-enabled;
// with it off, nothing binds, nothing is announced, nothing is writable.

package hvac

import (
	"fmt"
	"sort"
	"sync"

	"eebusd/internal/writes/wucapi"
	"github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	"github.com/enbility/eebus-go/usecases/ca/cdsf"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// fallbackModes is the static action list offered while the device has not
// (yet) announced its supported HVAC operation modes. The underlying write
// fails closed on a mode the device does not relate to its system function, so
// advertising the full set early is safe: worst case the user picks an
// unsupported mode and gets a clean command_result error.
var fallbackModes = []string{"auto", "eco", "off", "on"}

// shared lets the cdt module resolve the CURRENT DHW operation mode through
// the cdsf use case (CDT writes a setpoint per mode; the water heater's target
// temperature applies to the mode the circuit is currently in). The modules
// bind in registry order, so the link is resolved lazily at dispatch time,
// never at Bind time.
var shared = struct {
	mu   sync.Mutex
	cdsf *cdsf.CDSF
}{}

// setSharedCDSF records the bound CDSF implementation for cross-module lookups.
func setSharedCDSF(impl *cdsf.CDSF) {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	shared.cdsf = impl
}

// currentDHWMode resolves the current DHW operation mode via the bound CDSF
// use case. Fails closed when the mode is not (yet) known.
func currentDHWMode(entity spineapi.EntityRemoteInterface) (ucapi.HvacOperationModeType, error) {
	shared.mu.Lock()
	impl := shared.cdsf
	shared.mu.Unlock()
	if impl == nil {
		return "", fmt.Errorf("hvac: cdsf use case not bound")
	}
	mode, err := impl.CurrentOperationMode(entity)
	if err != nil {
		return "", fmt.Errorf("hvac: current DHW mode unknown: %w", err)
	}
	return mode, nil
}

// sortedModes renders the supported operation modes as a sorted, deduplicated
// string slice (stable HA select options). Returns fallbackModes on error.
func sortedModes(modes []ucapi.HvacOperationModeType, err error) []string {
	if err != nil || len(modes) == 0 {
		return append([]string(nil), fallbackModes...)
	}
	seen := make(map[string]struct{}, len(modes))
	out := make([]string, 0, len(modes))
	for _, m := range modes {
		s := string(m)
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// adaptResultCB bridges the eebus-go result callback signature to the
// wucapi.ResultCB signature (same mapping as the ohpcf module: a non-zero
// SPINE error number is an error, an optional description overrides the
// generic reason).
func adaptResultCB(cb wucapi.ResultCB) func(model.ResultDataType, model.MsgCounterType) {
	if cb == nil {
		return nil
	}
	return func(result model.ResultDataType, msgCounter model.MsgCounterType) {
		errStr := ""
		status := wucapi.ResultOK
		if result.ErrorNumber != nil && *result.ErrorNumber != model.ErrorNumberTypeNoError {
			status = wucapi.ResultError
			errStr = fmt.Sprintf("device rejected (error %d)", uint(*result.ErrorNumber))
			if result.Description != nil && *result.Description != "" {
				errStr = string(*result.Description)
			}
		}
		var mc *uint32
		if msgCounter != 0 {
			v := uint32(msgCounter)
			mc = &v
		}
		cb(status, mc, errStr)
	}
}

// formatFloat renders a setpoint compactly (integers without decimals,
// fractional with one decimal trimmed), mirroring the ohpcf formatter so the
// number entity states look consistent across use cases.
func formatFloat(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	s := fmt.Sprintf("%.1f", v)
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}

// eventRouter builds the eebus-go event callback shared by all four modules:
// the use case's own UseCaseSupportUpdate (compatibility change) and any
// listed data update (which refreshes the entity state) fire the daemon Event
// callback so the controllable line is re-emitted with fresh state/actions.
// The daemon re-loops every compatible use case on each event, so a mode or
// setpoint change refreshes all HVAC entities at once.
func eventRouter(eventCB wucapi.EventCallback, support api.EventType, updates ...api.EventType) api.EntityEventCallback {
	return api.EntityEventCallback(func(ski string, _ spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event api.EventType) {
		if eventCB == nil {
			return
		}
		if event == support {
			eventCB(ski, entity)
			return
		}
		for _, u := range updates {
			if event == u {
				eventCB(ski, entity)
				return
			}
		}
	})
}
