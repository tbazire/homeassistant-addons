// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package scanner: export.go — the presentation layer (NDJSON + text tables).
//
// Separated from scanner.go on purpose:
//   - scanner.go decides WHAT to read from the remote device (state, requests);
//   - export.go    decides HOW to render it (human-readable table or NDJSON).
//
// NDJSON contract (consumed by eebus-bridge):
// In -json mode each emitted line is a self-contained JSON object carrying a
// discriminant "kind" field. The bridge parses "kind" to route the payload.
//
//	{"kind":"device",        ...}  // announced once per (ski, entity)
//	{"kind":"manufacturer",  ...}  // brand/model/serial/revisions
//	{"kind":"configuration", ...}  // one line per key/value
//	{"kind":"measurement",   ...}  // one line per measurement value
//	{"kind":"diagnosis",     ...}  // operating state, last error, uptime
//
// Every line carries the remote "ski" so the bridge can group all entities of
// one physical device under a single Home Assistant device. The SKI is an
// identifier (public), not a secret.

package scanner

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/enbility/eebus-go/features/client"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// NDJSON line "kind" discriminants. These strings ARE the wire contract with
// eebus-bridge: renaming one is a breaking change for the consumer.
const (
	kindDevice        = "device"
	kindManufacturer  = "manufacturer"
	kindConfiguration = "configuration"
	kindMeasurement   = "measurement"
	kindDiagnosis     = "diagnosis"
)

// ---- NDJSON payload structs ------------------------------------------------

// envelope holds the routing metadata common to every emitted line. It is
// embedded by each typed line struct so the JSON output has a stable shape.
type envelope struct {
	Kind   string `json:"kind"`
	SKI    string `json:"ski"`
	Entity string `json:"entity"`
	Time   string `json:"time"`
}

// withKind returns a copy of the envelope with the kind field set. Each
// render*JSON builder calls this to stamp its discriminant.
func (e envelope) withKind(k string) envelope {
	e.Kind = k
	return e
}

type deviceLine struct {
	envelope
	EntityType string `json:"entity_type"`
}

type manufacturerLine struct {
	envelope
	DeviceName       string `json:"device_name,omitempty"`
	DeviceCode       string `json:"device_code,omitempty"`
	SerialNumber     string `json:"serial,omitempty"`
	BrandName        string `json:"brand_name,omitempty"`
	VendorName       string `json:"vendor_name,omitempty"`
	VendorCode       string `json:"vendor_code,omitempty"`
	SoftwareRevision string `json:"sw_version,omitempty"`
	HardwareRevision string `json:"hw_version,omitempty"`
}

type configurationLine struct {
	envelope
	KeyID     string `json:"key_id"`
	KeyName   string `json:"key_name,omitempty"`
	Value     string `json:"value,omitempty"`
	ValueType string `json:"value_type,omitempty"`
}

type measurementLine struct {
	envelope
	ID        string  `json:"id"`
	Type      string  `json:"type,omitempty"`
	Commodity string  `json:"commodity,omitempty"`
	Scope     string  `json:"scope,omitempty"`
	Unit      string  `json:"unit,omitempty"`
	Value     float64 `json:"value,omitempty"`
	Scale     int     `json:"scale,omitempty"`
}

type diagnosisLine struct {
	envelope
	OperatingState string `json:"operating_state,omitempty"`
	LastErrorCode  string `json:"last_error_code,omitempty"`
	UpTime         string `json:"up_time,omitempty"`
}

// ---- Renderer dispatch -----------------------------------------------------

// renderEntity walks the cached helpers for one entity and renders them.
// In JSON mode it writes NDJSON lines to s.dataOut; in text mode it writes a
// human-readable table via the package logger.
//
// This is the single point that Scanner.printEntityData delegates to; keeping
// it here leaves scanner.go focused on state, not presentation.
func (s *Scanner) renderEntity(tracker *remoteTracker, addr string, entity spineapi.EntityRemoteInterface) {
	if s.options.JSONOut {
		s.renderEntityJSON(tracker, addr, entity)
		return
	}
	s.renderEntityText(tracker, addr, entity)
}

// ---- JSON mode -------------------------------------------------------------

func (s *Scanner) renderEntityJSON(tracker *remoteTracker, addr string, entity spineapi.EntityRemoteInterface) {
	env := envelope{SKI: tracker.ski, Entity: addr, Time: time.Now().UTC().Format(time.RFC3339Nano)}

	// device line: one per (ski, entity), announcing the entity type.
	if entity != nil {
		s.writeJSON(deviceLine{
			envelope:   env.withKind(kindDevice),
			EntityType: string(entity.EntityType()),
		})
	}

	if dc := tracker.classifications[addr]; dc != nil {
		s.renderManufacturerJSON(env, dc)
	}
	if dc := tracker.configurations[addr]; dc != nil {
		s.renderConfigurationJSON(env, dc)
	}
	if m := tracker.measurements[addr]; m != nil {
		s.renderMeasurementsJSON(env, m)
	}
	// Diagnosis helper is created lazily from the real entity.
	if entity != nil {
		if dd, err := client.NewDeviceDiagnosis(s.localEntity, entity); err == nil && dd != nil {
			s.renderDiagnosisJSON(env, dd)
		}
	}
}

func (s *Scanner) renderManufacturerJSON(env envelope, dc *client.DeviceClassification) {
	details, err := dc.GetManufacturerDetails()
	if err != nil || details == nil {
		return
	}
	line := manufacturerLine{envelope: env.withKind(kindManufacturer)}
	if details.DeviceName != nil {
		line.DeviceName = string(*details.DeviceName)
	}
	if details.DeviceCode != nil {
		line.DeviceCode = string(*details.DeviceCode)
	}
	if details.SerialNumber != nil {
		line.SerialNumber = string(*details.SerialNumber)
	}
	if details.BrandName != nil {
		line.BrandName = string(*details.BrandName)
	}
	if details.VendorName != nil {
		line.VendorName = string(*details.VendorName)
	}
	if details.VendorCode != nil {
		line.VendorCode = string(*details.VendorCode)
	}
	if details.SoftwareRevision != nil {
		line.SoftwareRevision = string(*details.SoftwareRevision)
	}
	if details.HardwareRevision != nil {
		line.HardwareRevision = string(*details.HardwareRevision)
	}
	s.writeJSON(line)
}

func (s *Scanner) renderConfigurationJSON(env envelope, dc *client.DeviceConfiguration) {
	descs, err := dc.GetKeyValueDescriptionsForFilter(model.DeviceConfigurationKeyValueDescriptionDataType{})
	if err != nil {
		return
	}
	for _, d := range descs {
		line := configurationLine{envelope: env.withKind(kindConfiguration)}
		if d.KeyId != nil {
			line.KeyID = strconv.FormatUint(uint64(*d.KeyId), 10)
		}
		if d.KeyName != nil {
			line.KeyName = string(*d.KeyName)
		}
		if d.ValueType != nil {
			line.ValueType = string(*d.ValueType)
		}
		if d.KeyId != nil {
			if value, err := dc.GetKeyValueDataForKeyId(*d.KeyId); err == nil && value != nil && value.Value != nil {
				val, _ := configValueToJSON(value.Value)
				line.Value = val
				line.ValueType = configValueType(value.Value, line.ValueType)
			}
		}
		s.writeJSON(line)
	}
}

func (s *Scanner) renderMeasurementsJSON(env envelope, m *client.Measurement) {
	descs, err := m.GetDescriptionsForFilter(model.MeasurementDescriptionDataType{})
	if err != nil {
		return
	}
	descByID := make(map[model.MeasurementIdType]model.MeasurementDescriptionDataType, len(descs))
	for _, d := range descs {
		if d.MeasurementId != nil {
			descByID[*d.MeasurementId] = d
		}
	}
	data, _ := m.GetDataForFilter(model.MeasurementDescriptionDataType{})
	for _, d := range data {
		desc := descByID[*d.MeasurementId]
		line := measurementLine{
			envelope: env.withKind(kindMeasurement),
			ID:       idStr(d.MeasurementId),
		}
		if desc.MeasurementType != nil {
			line.Type = string(*desc.MeasurementType)
		}
		if desc.CommodityType != nil {
			line.Commodity = string(*desc.CommodityType)
		}
		if desc.ScopeType != nil {
			line.Scope = string(*desc.ScopeType)
		}
		if desc.Unit != nil {
			line.Unit = string(*desc.Unit)
		}
		if d.Value != nil {
			line.Value = d.Value.GetValue()
			if d.Value.Scale != nil {
				line.Scale = int(*d.Value.Scale)
			}
		}
		s.writeJSON(line)
	}
}

func (s *Scanner) renderDiagnosisJSON(env envelope, dd *client.DeviceDiagnosis) {
	state, err := dd.GetState()
	if err != nil || state == nil {
		return
	}
	line := diagnosisLine{envelope: env.withKind(kindDiagnosis)}
	if state.OperatingState != nil {
		line.OperatingState = string(*state.OperatingState)
	}
	if state.LastErrorCode != nil {
		line.LastErrorCode = string(*state.LastErrorCode)
	}
	if state.UpTime != nil {
		line.UpTime = string(*state.UpTime)
	}
	s.writeJSON(line)
}

// writeJSON marshals one payload to a single line and writes it to s.dataOut.
// On marshal error it logs and continues (one bad line must not abort the stream).
func (s *Scanner) writeJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		logWarnf("export: json marshal: %v", err)
		return
	}
	w := s.dataOut
	if w == nil {
		w = os.Stdout
	}
	_, _ = w.Write(append(b, '\n'))
}

// ---- Text mode (default, unchanged from V1) --------------------------------

func (s *Scanner) renderEntityText(tracker *remoteTracker, addr string, entity spineapi.EntityRemoteInterface) {
	if dc := tracker.classifications[addr]; dc != nil {
		s.printManufacturerDetails(addr, dc)
	}
	if dc := tracker.configurations[addr]; dc != nil {
		s.printDeviceConfiguration(addr, dc)
	}
	if m := tracker.measurements[addr]; m != nil {
		s.printMeasurements(addr, m)
	}
	if entity != nil {
		if dd, err := client.NewDeviceDiagnosis(s.localEntity, entity); err == nil && dd != nil {
			s.printDeviceDiagnosis(addr, dd)
		}
	}
}

func (s *Scanner) printManufacturerDetails(addr string, dc *client.DeviceClassification) {
	details, err := dc.GetManufacturerDetails()
	if err != nil || details == nil {
		return
	}
	logInfof("  [%s] manufacturer:", addr)
	if details.DeviceName != nil {
		logInfof("    name:         %s", *details.DeviceName)
	}
	if details.DeviceCode != nil {
		logInfof("    code:         %s", *details.DeviceCode)
	}
	if details.SerialNumber != nil {
		logInfof("    serial:       %s", *details.SerialNumber)
	}
	if details.BrandName != nil {
		logInfof("    brand:        %s", *details.BrandName)
	}
	if details.VendorName != nil {
		logInfof("    vendor:       %s", *details.VendorName)
	}
	if details.VendorCode != nil {
		logInfof("    vendor code:  %s", *details.VendorCode)
	}
	if details.SoftwareRevision != nil {
		logInfof("    software:     %s", *details.SoftwareRevision)
	}
	if details.HardwareRevision != nil {
		logInfof("    hardware:     %s", *details.HardwareRevision)
	}
}

func (s *Scanner) printDeviceConfiguration(addr string, dc *client.DeviceConfiguration) {
	descs, err := dc.GetKeyValueDescriptionsForFilter(model.DeviceConfigurationKeyValueDescriptionDataType{})
	if err != nil || len(descs) == 0 {
		return
	}
	logInfof("  [%s] device configuration (%d entries):", addr, len(descs))
	for _, d := range descs {
		if d.KeyName == nil {
			continue
		}
		line := fmt.Sprintf("    %s", *d.KeyName)
		if value, err := dc.GetKeyValueDataForKeyId(*d.KeyId); err == nil && value != nil && value.Value != nil {
			line += " = " + formatConfigValue(value.Value)
		}
		logInfof("%s", line)
	}
}

func (s *Scanner) printDeviceDiagnosis(addr string, dd *client.DeviceDiagnosis) {
	state, err := dd.GetState()
	if err != nil || state == nil {
		return
	}
	parts := []string{}
	if state.OperatingState != nil {
		parts = append(parts, fmt.Sprintf("state=%s", *state.OperatingState))
	}
	if state.LastErrorCode != nil {
		parts = append(parts, fmt.Sprintf("lastError=%s", *state.LastErrorCode))
	}
	if state.UpTime != nil {
		parts = append(parts, fmt.Sprintf("upTime=%s", *state.UpTime))
	}
	logInfof("  [%s] diagnosis: %s", addr, strings.Join(parts, " "))
}

// printMeasurements renders the current measurement cache as a text table.
func (s *Scanner) printMeasurements(addr string, m *client.Measurement) {
	descs, err := m.GetDescriptionsForFilter(model.MeasurementDescriptionDataType{})
	if err != nil || len(descs) == 0 {
		return
	}
	descByID := make(map[model.MeasurementIdType]model.MeasurementDescriptionDataType, len(descs))
	for _, d := range descs {
		if d.MeasurementId != nil {
			descByID[*d.MeasurementId] = d
		}
	}
	data, _ := m.GetDataForFilter(model.MeasurementDescriptionDataType{})
	if len(data) == 0 {
		logInfof("  [%s] measurements: %d descriptors, no values yet:", addr, len(descs))
		for _, d := range descs {
			logInfof("    id=%s %s", idStr(d.MeasurementId), describeDescription(d))
		}
		return
	}
	logInfof("  [%s] measurements (%d values):", addr, len(data))
	for _, d := range data {
		label := describeDescription(descByID[*d.MeasurementId])
		value := "<none>"
		if d.Value != nil {
			value = fmt.Sprintf("%.6g", d.Value.GetValue())
		}
		logInfof("    id=%s %s = %s", idStr(d.MeasurementId), label, value)
	}
}

// ---- Helpers ---------------------------------------------------------------

// idStr formats a measurement id as a decimal string.
func idStr(id *model.MeasurementIdType) string {
	if id == nil {
		return "?"
	}
	return strconv.FormatUint(uint64(*id), 10)
}

func describeDescription(desc model.MeasurementDescriptionDataType) string {
	var parts []string
	if desc.MeasurementType != nil {
		parts = append(parts, fmt.Sprintf("type=%s", *desc.MeasurementType))
	}
	if desc.CommodityType != nil {
		parts = append(parts, fmt.Sprintf("commodity=%s", *desc.CommodityType))
	}
	if desc.ScopeType != nil {
		parts = append(parts, fmt.Sprintf("scope=%s", *desc.ScopeType))
	}
	if desc.Unit != nil {
		parts = append(parts, fmt.Sprintf("unit=%s", *desc.Unit))
	}
	return strings.Join(parts, " ")
}

// formatConfigValue renders the active field of a DeviceConfigurationKeyValueValueType
// as a human-readable string (text mode).
func formatConfigValue(v *model.DeviceConfigurationKeyValueValueType) string {
	if v == nil {
		return "<none>"
	}
	switch {
	case v.ScaledNumber != nil:
		return fmt.Sprintf("%g", v.ScaledNumber.GetValue())
	case v.Boolean != nil:
		return fmt.Sprintf("%v", *v.Boolean)
	case v.String != nil:
		return string(*v.String)
	case v.Integer != nil:
		return fmt.Sprintf("%d", *v.Integer)
	case v.Duration != nil:
		return string(*v.Duration)
	case v.DateTime != nil:
		return string(*v.DateTime)
	case v.Date != nil:
		return string(*v.Date)
	case v.Time != nil:
		return string(*v.Time)
	default:
		return "<unknown>"
	}
}

// configValueToJSON renders the active field as a JSON-friendly string and
// returns it together with a normalized type tag.
func configValueToJSON(v *model.DeviceConfigurationKeyValueValueType) (string, string) {
	if v == nil {
		return "", ""
	}
	switch {
	case v.ScaledNumber != nil:
		return fmt.Sprintf("%g", v.ScaledNumber.GetValue()), "scaled_number"
	case v.Boolean != nil:
		return fmt.Sprintf("%v", *v.Boolean), "boolean"
	case v.String != nil:
		return string(*v.String), "string"
	case v.Integer != nil:
		return fmt.Sprintf("%d", *v.Integer), "integer"
	case v.Duration != nil:
		return string(*v.Duration), "duration"
	case v.DateTime != nil:
		return string(*v.DateTime), "date_time"
	case v.Date != nil:
		return string(*v.Date), "date"
	case v.Time != nil:
		return string(*v.Time), "time"
	default:
		return "", "unknown"
	}
}

// configValueType prefers the type declared by the descriptor; falls back to
// inferring it from the active value field when the descriptor omits it.
func configValueType(v *model.DeviceConfigurationKeyValueValueType, declared string) string {
	if declared != "" {
		return declared
	}
	_, inferred := configValueToJSON(v)
	return inferred
}

// SetDataOut overrides the NDJSON data sink (defaults to stdout). Exposed for
// tests and for future alternate sinks.
func (s *Scanner) SetDataOut(w io.Writer) {
	if w != nil {
		s.dataOut = w
	}
}
