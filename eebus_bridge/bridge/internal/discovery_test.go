// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Tests for the EEBUS → HA mapping (pure logic, no MQTT).

package internal

import (
	"strings"
	"testing"
)

func TestUniqueIDStableAndScoped(t *testing.T) {
	a := uniqueID("aaaabbbbccccddddeeee00001111222233334444", "3.1", "5")
	b := uniqueID("aaaabbbbccccddddeeee00001111222233334444", "3.1", "5")
	if a != b {
		t.Errorf("uniqueID not stable: %q vs %q", a, b)
	}
	// Different entity or id must produce a different uniqueID.
	c := uniqueID("aaaabbbbccccddddeeee00001111222233334444", "3.2", "5")
	d := uniqueID("aaaabbbbccccddddeeee00001111222233334444", "3.1", "6")
	if c == a || d == a {
		t.Errorf("uniqueID collision: %q %q %q", a, c, d)
	}
	// Different SKI must produce a different uniqueID.
	e := uniqueID("1111222233334444555566667777888899990000", "3.1", "5")
	if e == a {
		t.Errorf("uniqueID collision across SKIs")
	}
}

func TestEntitySafeReplacesDots(t *testing.T) {
	if got := entitySafe("3.1"); got != "3_1" {
		t.Errorf("entitySafe = %q, want 3_1", got)
	}
}

func TestOnManufacturerBuildsDevice(t *testing.T) {
	m := NewMapper("eebus", "homeassistant")
	d := m.OnManufacturer(&Manufacturer{
		Line:             Line{SKI: "aaaabbbbccccddddeeee00001111222233334444"},
		DeviceName:       "GeniaAir Mono",
		BrandName:        "Saunier Duval",
		SoftwareRevision: "1.2.3",
	})
	if d.Name != "GeniaAir Mono" || d.Manufacturer != "Saunier Duval" || d.SWVersion != "1.2.3" {
		t.Errorf("device not populated: %+v", d)
	}
	if len(d.Identifiers) != 1 || d.Identifiers[0] != "aaaabbbbccccddddeeee00001111222233334444" {
		t.Errorf("identifiers wrong: %v", d.Identifiers)
	}
}

func TestOnMeasurementFirstCallPublishesDiscovery(t *testing.T) {
	m := NewMapper("eebus", "homeassistant")
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	// Pre-seed a device so the sensor attaches to it.
	m.OnManufacturer(&Manufacturer{Line: Line{SKI: ski}, BrandName: "Brand", DeviceName: "Model"})

	me := &Measurement{
		Line:  Line{SKI: ski, Entity: "3.1"},
		ID:    "5",
		Type:  "Power",
		Scope: "AC-Output",
		Unit:  "W",
		Value: 1234.5,
	}
	disc := m.OnMeasurement(me)
	if disc.Config == nil {
		t.Fatal("first call must publish discovery")
	}
	if disc.ConfigTopic == "" || !strings.Contains(disc.ConfigTopic, "homeassistant/sensor/eebus_bridge/") {
		t.Errorf("config topic wrong: %q", disc.ConfigTopic)
	}
	if disc.Config.UnitOfMeasurement != "W" || disc.Config.DeviceClass != "power" {
		t.Errorf("unit/class wrong: unit=%q class=%q",
			disc.Config.UnitOfMeasurement, disc.Config.DeviceClass)
	}
	if disc.Config.Device == nil || disc.Config.Device.Name != "Model" {
		t.Errorf("device not attached: %+v", disc.Config.Device)
	}
	if disc.StateValue == "" || !strings.Contains(disc.StateValue, "1234") {
		t.Errorf("state value wrong: %q", disc.StateValue)
	}
}

func TestOnMeasurementSecondCallSkipsDiscovery(t *testing.T) {
	m := NewMapper("eebus", "homeassistant")
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	me := &Measurement{Line: Line{SKI: ski, Entity: "3.1"}, ID: "5", Value: 1}
	m.OnMeasurement(me)
	disc := m.OnMeasurement(me)
	if disc.Config != nil {
		t.Errorf("second call must NOT publish discovery (already announced)")
	}
	if disc.StateValue == "" {
		t.Errorf("second call must still publish state")
	}
}

func TestDeviceClassAndStateClass(t *testing.T) {
	cases := []struct {
		typ, unit, wantDev, wantState string
	}{
		{"Power", "W", "power", "measurement"},
		{"Energy", "WH", "energy", "total_increasing"},
		{"Voltage", "V", "voltage", "measurement"},
		{"Current", "A", "current", "measurement"},
		{"Frequency", "HZ", "frequency", "measurement"},
		{"Unknown", "", "", "measurement"},
	}
	for _, c := range cases {
		if got := deviceClassFor(c.typ, c.unit); got != c.wantDev {
			t.Errorf("deviceClassFor(%q,%q) = %q, want %q", c.typ, c.unit, got, c.wantDev)
		}
		if got := stateClassFor(c.typ); got != c.wantState {
			t.Errorf("stateClassFor(%q) = %q, want %q", c.typ, got, c.wantState)
		}
	}
}

func TestUnitOfMeasurement(t *testing.T) {
	cases := map[string]string{
		"W":       "W",
		"WH":      "Wh",
		"KWH":     "kWh",
		"A":       "A",
		"C":       "°C",
		"PERCENT": "%",
		"FOOBAR":  "FOOBAR", // unknown passes through
	}
	for in, want := range cases {
		if got := unitOfMeasurement(in); got != want {
			t.Errorf("unitOfMeasurement(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatValue(t *testing.T) {
	cases := []struct {
		v    float64
		want string
	}{
		{1234.5, "1234.5"},
		{100, "100"},
		{0.5, "0.5"},
		{0, "0"},
	}
	for _, c := range cases {
		if got := formatValue(c.v, 0); got != c.want {
			t.Errorf("formatValue(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}
