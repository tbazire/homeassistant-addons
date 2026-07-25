// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Tests for the NDJSON presentation layer (export.go).
//
// These tests validate the wire format that eebus-bridge consumes. They are
// "golden" tests: the expected JSON is hardcoded and any drift in the schema
// is a deliberate, reviewable change (or a regression to catch).
//
// We test the serializable structs directly rather than the render*JSON
// methods, because those methods depend on features/client types that cannot
// be constructed without a live SPINE pairing. Testing the structs covers the
// part that matters for the wire contract: field names, omitempty rules,
// kind discriminants, and value/type rendering.

package scanner

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/enbility/spine-go/model"
)

// mustMarshal marshals v and fails the test on error.
func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// assertJSON marshals got and compares it to want as a single-line string.
// The comparison is on the raw JSON text to make schema drift obvious in diffs.
func assertJSON(t *testing.T, got any, want string) {
	t.Helper()
	g := mustMarshal(t, got)
	if g != want {
		t.Errorf("JSON mismatch\n got: %s\nwant: %s", g, want)
	}
}

func TestEnvelopeWithKind(t *testing.T) {
	e := envelope{SKI: "abc", Entity: "0", Time: "2026-01-01T00:00:00Z"}
	e2 := e.withKind(kindDevice)
	if e2.Kind != kindDevice {
		t.Errorf("kind = %q, want %q", e2.Kind, kindDevice)
	}
	// Original must be unchanged (value receiver).
	if e.Kind != "" {
		t.Errorf("original envelope mutated: kind=%q", e.Kind)
	}
}

func TestMeasurementLineSerialization(t *testing.T) {
	line := measurementLine{
		envelope:  envelope{Kind: kindMeasurement, SKI: "ski1", Entity: "3.1", Time: "t"},
		ID:        "5",
		Type:      "Power",
		Commodity: "Electricity",
		Scope:     "AC-Output",
		Unit:      "W",
		Value:     1234.5,
		Scale:     0,
	}
	got := mustMarshal(t, line)
	// Required fields present.
	for _, want := range []string{`"kind":"measurement"`, `"ski":"ski1"`, `"entity":"3.1"`, `"id":"5"`, `"value":1234.5`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

func TestMeasurementLineOmitempty(t *testing.T) {
	// A measurement with no value must omit value/scale/type/etc.
	line := measurementLine{
		envelope: envelope{Kind: kindMeasurement, SKI: "s", Entity: "0", Time: "t"},
		ID:       "1",
	}
	got := mustMarshal(t, line)
	for _, unwanted := range []string{`"value"`, `"scale"`, `"type"`, `"unit"`} {
		if strings.Contains(got, unwanted) {
			t.Errorf("unexpected %q in %s", unwanted, got)
		}
	}
}

func TestManufacturerLineSerialization(t *testing.T) {
	line := manufacturerLine{
		envelope:         envelope{Kind: kindManufacturer, SKI: "s", Entity: "0", Time: "t"},
		BrandName:        "Saunier Duval",
		DeviceName:       "GeniaAir Mono",
		SerialNumber:     "SN12345",
		SoftwareRevision: "1.2.3",
	}
	got := mustMarshal(t, line)
	for _, want := range []string{
		`"kind":"manufacturer"`,
		`"brand_name":"Saunier Duval"`,
		`"device_name":"GeniaAir Mono"`,
		`"serial":"SN12345"`,
		`"sw_version":"1.2.3"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	// Fields not set must be omitted (omitempty).
	for _, unwanted := range []string{`"hw_version"`, `"vendor_code"`, `"device_code"`} {
		if strings.Contains(got, unwanted) {
			t.Errorf("unexpected %q in %s", unwanted, got)
		}
	}
}

func TestConfigurationLineSerialization(t *testing.T) {
	line := configurationLine{
		envelope:  envelope{Kind: kindConfiguration, SKI: "s", Entity: "0", Time: "t"},
		KeyID:     "5",
		KeyName:   "Heartbeat",
		Value:     "300",
		ValueType: "integer",
	}
	assertJSON(t, line, `{"kind":"configuration","ski":"s","entity":"0","time":"t","key_id":"5","key_name":"Heartbeat","value":"300","value_type":"integer"}`)
}

func TestDiagnosisLineSerialization(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		line := diagnosisLine{
			envelope:       envelope{Kind: kindDiagnosis, SKI: "s", Entity: "0", Time: "t"},
			OperatingState: "normalOperation",
			LastErrorCode:  "",
			UpTime:         "PT1H",
		}
		got := mustMarshal(t, line)
		// operating_state present; last_error_code omitted (empty + omitempty).
		if !strings.Contains(got, `"operating_state":"normalOperation"`) {
			t.Errorf("missing operating_state in %s", got)
		}
		if strings.Contains(got, "last_error_code") {
			t.Errorf("unexpected last_error_code in %s", got)
		}
	})
}

func TestDeviceLineSerialization(t *testing.T) {
	line := deviceLine{
		envelope:   envelope{Kind: kindDevice, SKI: "s", Entity: "0", Time: "t"},
		EntityType: "DeviceInformation",
	}
	assertJSON(t, line, `{"kind":"device","ski":"s","entity":"0","time":"t","entity_type":"DeviceInformation"}`)
}

// ---- configValueToJSON / configValueType -----------------------------------

func TestConfigValueToJSON(t *testing.T) {
	cases := []struct {
		name     string
		value    *model.DeviceConfigurationKeyValueValueType
		wantVal  string
		wantType string
	}{
		{
			name:     "nil",
			value:    nil,
			wantVal:  "",
			wantType: "",
		},
		{
			name:     "integer",
			value:    &model.DeviceConfigurationKeyValueValueType{Integer: ptrInt(300)},
			wantVal:  "300",
			wantType: "integer",
		},
		{
			name:     "boolean",
			value:    &model.DeviceConfigurationKeyValueValueType{Boolean: ptrBool(true)},
			wantVal:  "true",
			wantType: "boolean",
		},
		{
			name:     "string",
			value:    &model.DeviceConfigurationKeyValueValueType{String: ptrConfigStr("abc")},
			wantVal:  "abc",
			wantType: "string",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			val, typ := configValueToJSON(c.value)
			if val != c.wantVal {
				t.Errorf("value = %q, want %q", val, c.wantVal)
			}
			if typ != c.wantType {
				t.Errorf("type = %q, want %q", typ, c.wantType)
			}
		})
	}
}

func TestConfigValueTypePrefersDeclared(t *testing.T) {
	v := &model.DeviceConfigurationKeyValueValueType{Integer: ptrInt(1)}
	// Declared type wins even if it disagrees with the active field.
	if got := configValueType(v, "duration"); got != "duration" {
		t.Errorf("configValueType = %q, want duration (declared)", got)
	}
	// Without a declared type, the value field is inferred.
	if got := configValueType(v, ""); got != "integer" {
		t.Errorf("configValueType = %q, want integer (inferred)", got)
	}
}

// ---- writeJSON via Scanner ------------------------------------------------

// TestScannerWriteJSON checks that writeJSON emits exactly one newline-terminated
// line and that the JSON content is valid.
func TestScannerWriteJSON(t *testing.T) {
	var sb strings.Builder
	s := &Scanner{dataOut: &sb}
	s.writeJSON(measurementLine{
		envelope: envelope{Kind: kindMeasurement, SKI: "s", Entity: "0", Time: "t"},
		ID:       "1",
		Value:    42,
	})
	out := sb.String()
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output not newline-terminated: %q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("expected exactly 1 line, got %d in %q", strings.Count(out, "\n"), out)
	}
	// Must be valid JSON (parses back into a measurementLine).
	var back measurementLine
	if err := json.Unmarshal([]byte(out), &back); err != nil {
		t.Errorf("unmarshal: %v", err)
	}
	if back.Value != 42 || back.ID != "1" || back.Kind != kindMeasurement {
		t.Errorf("round-trip mismatch: %+v", back)
	}
}

// ---- helpers for building model values in tests ---------------------------

func ptrInt(i int64) *int64 { return &i }

func ptrBool(b bool) *bool { return &b }

func ptrConfigStr(s string) *model.DeviceConfigurationKeyValueStringType {
	v := model.DeviceConfigurationKeyValueStringType(s)
	return &v
}

// ---- normalizeUnit tests ----------------------------------------------------

func TestNormalizeUnit(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"degC to °C", "degC", "°C"},
		{"DEGC uppercase", "DEGC", "°C"},
		{"degF to °F", "degF", "°F"},
		{"W unchanged", "W", "W"},
		{"kW unchanged", "kW", "kW"},
		{"empty string", "", ""},
		{"unknown unit", "xyz", "xyz"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizeUnit(c.got); got != c.want {
				t.Errorf("normalizeUnit(%q) = %q, want %q", c.got, got, c.want)
			}
		})
	}
}
