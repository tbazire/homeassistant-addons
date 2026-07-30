// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Tests for the use-case read-signal discovery path: OnUcSignal builds a typed
// sensor (or binary_sensor) for OHPCF read signals, with the right device class
// and unit, attached to the same device as the control entity.

package internal

import (
	"strings"
	"testing"
)

func TestOnUcSignal_Boolean_BinarySensor(t *testing.T) {
	m := NewMapper("eebus", "homeassistant")
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	m.OnManufacturer(&Manufacturer{Line: Line{SKI: ski}, BrandName: "SD", DeviceName: "VR920"})

	s := &UcSignal{
		Line:      Line{SKI: ski, Entity: "3.1"},
		UseCase:   "ohpcf",
		Signal:    "is_pausable",
		Value:     "true",
		ValueType: "boolean",
	}
	disc := m.OnUcSignal(s)
	if disc.Config == nil {
		t.Fatal("OnUcSignal must publish a discovery for a boolean signal")
	}
	bs, ok := disc.Config.(*HABinarySensor)
	if !ok {
		t.Fatalf("config payload is %T, want *HABinarySensor", disc.Config)
	}
	if !strings.Contains(disc.ConfigTopic, "homeassistant/binary_sensor/eebus_bridge/") {
		t.Errorf("config topic should be binary_sensor/, got: %q", disc.ConfigTopic)
	}
	if bs.PayloadOn != "true" || bs.PayloadOff != "false" {
		t.Errorf("payloads = on:%q off:%q, want true/false", bs.PayloadOn, bs.PayloadOff)
	}
	if bs.Device == nil || bs.Device.Name != "VR920" {
		t.Errorf("device not attached: %+v", bs.Device)
	}
	if disc.StateValue != "true" {
		t.Errorf("boolean state = %q, want true", disc.StateValue)
	}
}

func TestOnUcSignal_Power_Sensor(t *testing.T) {
	m := NewMapper("eebus", "homeassistant")
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	s := &UcSignal{
		Line:      Line{SKI: ski, Entity: "3.1"},
		UseCase:   "ohpcf",
		Signal:    "requested_power",
		Value:     "1500",
		ValueType: "number",
		Unit:      "W",
	}
	disc := m.OnUcSignal(s)
	if disc.Config == nil {
		t.Fatal("OnUcSignal must publish a discovery for a power signal")
	}
	sn, ok := disc.Config.(*HASensor)
	if !ok {
		t.Fatalf("config payload is %T, want *HASensor", disc.Config)
	}
	if !strings.Contains(disc.ConfigTopic, "homeassistant/sensor/eebus_bridge/") {
		t.Errorf("config topic should be sensor/, got: %q", disc.ConfigTopic)
	}
	if sn.DeviceClass != "power" {
		t.Errorf("device_class = %q, want power", sn.DeviceClass)
	}
	if sn.UnitOfMeasurement != "W" {
		t.Errorf("unit = %q, want W", sn.UnitOfMeasurement)
	}
	if sn.StateClass != "measurement" {
		t.Errorf("state_class = %q, want measurement", sn.StateClass)
	}
	if disc.StateValue != "1500" {
		t.Errorf("state = %q, want 1500", disc.StateValue)
	}
}

func TestOnUcSignal_Duration_ConvertsSecondsToMinutes(t *testing.T) {
	// Durations arrive on the wire as whole seconds; the HA duration sensor is
	// labelled in minutes, so the state value must be converted.
	m := NewMapper("eebus", "homeassistant")
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	s := &UcSignal{
		Line:      Line{SKI: ski, Entity: "3.1"},
		UseCase:   "ohpcf",
		Signal:    "min_run_duration",
		Value:     "1800", // 30 minutes
		ValueType: "duration",
		Unit:      "seconds",
	}
	disc := m.OnUcSignal(s)
	if disc.Config == nil {
		t.Fatal("OnUcSignal must publish a discovery for a duration signal")
	}
	sn := disc.Config.(*HASensor)
	if sn.DeviceClass != "duration" {
		t.Errorf("device_class = %q, want duration", sn.DeviceClass)
	}
	if sn.UnitOfMeasurement != "min" {
		t.Errorf("unit = %q, want min", sn.UnitOfMeasurement)
	}
	if disc.StateValue != "30" {
		t.Errorf("state = %q, want 30 (minutes)", disc.StateValue)
	}
}

func TestOnUcSignal_StartTime_TimestampSensor(t *testing.T) {
	m := NewMapper("eebus", "homeassistant")
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	ts := "2026-07-30T14:00:00Z"
	s := &UcSignal{
		Line:      Line{SKI: ski, Entity: "3.1"},
		UseCase:   "ohpcf",
		Signal:    "start_time",
		Value:     ts,
		ValueType: "date_time",
	}
	disc := m.OnUcSignal(s)
	if disc.Config == nil {
		t.Fatal("OnUcSignal must publish a discovery for start_time")
	}
	sn := disc.Config.(*HASensor)
	if sn.DeviceClass != "timestamp" {
		t.Errorf("device_class = %q, want timestamp", sn.DeviceClass)
	}
	if disc.StateValue != ts {
		t.Errorf("state = %q, want %q", disc.StateValue, ts)
	}
}

func TestOnUcSignal_Dedup(t *testing.T) {
	// Same (ski, entity, usecase, signal) must publish discovery once; later
	// lines only refresh the state.
	m := NewMapper("eebus", "homeassistant")
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	s := &UcSignal{
		Line:    Line{SKI: ski, Entity: "3.1"},
		UseCase: "ohpcf", Signal: "requested_power",
		Value: "1000", ValueType: "number", Unit: "W",
	}
	first := m.OnUcSignal(s)
	if first.Config == nil {
		t.Fatal("first call must publish")
	}
	s.Value = "2000"
	second := m.OnUcSignal(s)
	if second.Config != nil {
		t.Error("second call must NOT re-publish discovery")
	}
	if second.StateValue != "2000" {
		t.Errorf("refresh state = %q, want 2000", second.StateValue)
	}
}

func TestSignalDurationMinutes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0", "0"},
		{"59", "0"},
		{"60", "1"},
		{"1800", "30"},
		{"not-a-number", "not-a-number"}, // bad input passes through unchanged
	}
	for _, c := range cases {
		got := signalDurationMinutes(c.in)
		if got != c.want {
			t.Errorf("signalDurationMinutes(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
