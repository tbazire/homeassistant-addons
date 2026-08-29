// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Tests for the NDJSON parser. The parser must be robust: malformed lines and
// unknown kinds must be skipped, never propagated as errors.

package internal

import (
	"strings"
	"testing"
)

// fakeLogger is a no-op Logger for tests.
type fakeLogger struct{}

func (fakeLogger) Info(string, ...any)  {}
func (fakeLogger) Warn(string, ...any)  {}
func (fakeLogger) Debug(string, ...any) {}

func TestParserSkipsBlankLines(t *testing.T) {
	in := "\n\n  \n"
	p := NewParser(strings.NewReader(in), fakeLogger{})
	count := 0
	if err := p.Stream(func(Event) { count++ }); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 events, got %d", count)
	}
}

func TestParserHandlesEachKind(t *testing.T) {
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	lines := []string{
		`{"kind":"device","ski":"` + ski + `","entity":"0","time":"t","entity_type":"DeviceInformation"}`,
		`{"kind":"manufacturer","ski":"` + ski + `","entity":"0","time":"t","brand_name":"Brand","device_name":"Model"}`,
		`{"kind":"configuration","ski":"` + ski + `","entity":"0","time":"t","key_id":"5","key_name":"Heartbeat","value":"300","value_type":"integer"}`,
		`{"kind":"measurement","ski":"` + ski + `","entity":"3.1","time":"t","id":"5","type":"Power","unit":"W","value":1234.5}`,
		`{"kind":"diagnosis","ski":"` + ski + `","entity":"0","time":"t","operating_state":"normalOperation","up_time":"PT1H"}`,
	}
	p := NewParser(strings.NewReader(strings.Join(lines, "\n")), fakeLogger{})

	var got []Event
	if err := p.Stream(func(ev Event) { got = append(got, ev) }); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 events, got %d", len(got))
	}
	if got[0].Device == nil || got[0].Device.EntityType != "DeviceInformation" {
		t.Errorf("event 0 not a device: %+v", got[0])
	}
	if got[1].Manufacturer == nil || got[1].Manufacturer.BrandName != "Brand" {
		t.Errorf("event 1 not a manufacturer: %+v", got[1])
	}
	if got[2].Configuration == nil || got[2].Configuration.KeyName != "Heartbeat" {
		t.Errorf("event 2 not a configuration: %+v", got[2])
	}
	if got[3].Measurement == nil || *got[3].Measurement.Value != 1234.5 {
		t.Errorf("event 3 not a measurement: %+v", got[3])
	}
	if got[4].Diagnosis == nil || got[4].Diagnosis.OperatingState != "normalOperation" {
		t.Errorf("event 4 not a diagnosis: %+v", got[4])
	}
}

func TestParserSkipsMalformedLines(t *testing.T) {
	in := strings.Join([]string{
		`{"kind":"measurement","ski":"s","entity":"0","time":"t","id":"1","value":1}`,
		`not json at all`,
		`{"kind":"measurement","ski":"s","entity":"0","time":"t","id":"2","value":2}`,
		`{"no_kind":"x"}`,
		``, // blank
		`{"kind":"measurement","ski":"s","entity":"0","time":"t","id":"3","value":3}`,
		`{"kind":"future_unknown_kind","ski":"s","entity":"0","time":"t"}`,
	}, "\n")
	p := NewParser(strings.NewReader(in), fakeLogger{})

	var got []Event
	if err := p.Stream(func(ev Event) { got = append(got, ev) }); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 valid events, got %d", len(got))
	}
	for i, ev := range got {
		if ev.Measurement == nil {
			t.Errorf("event %d not a measurement", i)
		}
	}
}

// TestParserKeepsZeroAfterMalformed ensures a zero value is kept even when it
// follows a malformed line (regression: scanner.Buffer / state must not be
// confused by a bad line).
func TestParserKeepsZeroAfterMalformed(t *testing.T) {
	in := strings.Join([]string{
		`not json`,
		`{"kind":"measurement","ski":"s","entity":"0","time":"t","id":"1","type":"Power","unit":"W","value":0}`,
	}, "\n")
	p := NewParser(strings.NewReader(in), fakeLogger{})
	var got []Event
	if err := p.Stream(func(ev Event) { got = append(got, ev) }); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(got) != 1 || got[0].Measurement == nil || *got[0].Measurement.Value != 0 {
		t.Fatalf("expected 1 zero-value measurement, got %+v", got)
	}
}

func TestParserSkipsNullMeasurementValue(t *testing.T) {
	in := strings.Join([]string{
		`{"kind":"measurement","ski":"s","entity":"0","time":"t","id":"1","type":"Power","unit":"W","value":1}`,
		`{"kind":"measurement","ski":"s","entity":"0","time":"t","id":"2","type":"Power","unit":"W","value":null}`,
		`{"kind":"measurement","ski":"s","entity":"0","time":"t","id":"3","type":"Power","unit":"W","value":3}`,
	}, "\n")
	p := NewParser(strings.NewReader(in), fakeLogger{})

	var got []Event
	if err := p.Stream(func(ev Event) { got = append(got, ev) }); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// Should skip the null value measurement
	if len(got) != 2 {
		t.Fatalf("expected 2 valid events (null value skipped), got %d", len(got))
	}
	if got[0].Measurement == nil || *got[0].Measurement.Value != 1 {
		t.Errorf("first event should be measurement with value=1, got %+v", got[0])
	}
	if got[1].Measurement == nil || *got[1].Measurement.Value != 3 {
		t.Errorf("second event should be measurement with value=3, got %+v", got[1])
	}
}

func TestParserSkipsMissingMeasurementValue(t *testing.T) {
	in := strings.Join([]string{
		`{"kind":"measurement","ski":"s","entity":"0","time":"t","id":"1","type":"Power","unit":"W","value":1}`,
		`{"kind":"measurement","ski":"s","entity":"0","time":"t","id":"2","type":"Power","unit":"W"}`,
		`{"kind":"measurement","ski":"s","entity":"0","time":"t","id":"3","type":"Power","unit":"W","value":3}`,
	}, "\n")
	p := NewParser(strings.NewReader(in), fakeLogger{})

	var got []Event
	if err := p.Stream(func(ev Event) { got = append(got, ev) }); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// Should skip the measurement with missing value field
	if len(got) != 2 {
		t.Fatalf("expected 2 valid events (missing value skipped), got %d", len(got))
	}
	if got[0].Measurement == nil || *got[0].Measurement.Value != 1 {
		t.Errorf("first event should be measurement with value=1, got %+v", got[0])
	}
	if got[1].Measurement == nil || *got[1].Measurement.Value != 3 {
		t.Errorf("second event should be measurement with value=3, got %+v", got[1])
	}
}

// TestParserKeepsZeroValue is the regression test for the "value=0 dropped"
// bug. A measurement whose value is exactly 0 (e.g. idle power on a heat pump)
// MUST be kept and forwarded to the mapper, not silently dropped as "no data".
// This was previously broken because Value was a float64 with omitempty, which
// conflated "0" with "absent".
func TestParserKeepsZeroValue(t *testing.T) {
	in := strings.Join([]string{
		`{"kind":"measurement","ski":"s","entity":"0","time":"t","id":"1","type":"Power","unit":"W","value":0}`,
		`{"kind":"measurement","ski":"s","entity":"0","time":"t","id":"2","type":"Temperature","unit":"°C","value":0}`,
		`{"kind":"measurement","ski":"s","entity":"0","time":"t","id":"3","type":"Energy","unit":"Wh","value":0}`,
	}, "\n")
	p := NewParser(strings.NewReader(in), fakeLogger{})

	var got []Event
	if err := p.Stream(func(ev Event) { got = append(got, ev) }); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 events (zero values MUST be kept), got %d", len(got))
	}
	for i, ev := range got {
		if ev.Measurement == nil {
			t.Fatalf("event %d not a measurement", i)
		}
		if ev.Measurement.Value == nil {
			t.Fatalf("event %d: value pointer is nil (should be 0)", i)
		}
		if *ev.Measurement.Value != 0 {
			t.Errorf("event %d: value = %v, want 0", i, *ev.Measurement.Value)
		}
	}
}

// TestParserKeepsNegativeValue ensures negative numbers (export, temperature
// below zero) are also preserved end-to-end.
func TestParserKeepsNegativeValue(t *testing.T) {
	in := `{"kind":"measurement","ski":"s","entity":"0","time":"t","id":"1","type":"Power","unit":"W","value":-1234.5}`
	p := NewParser(strings.NewReader(in), fakeLogger{})

	var got []Event
	if err := p.Stream(func(ev Event) { got = append(got, ev) }); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(got) != 1 || got[0].Measurement == nil {
		t.Fatalf("expected 1 measurement event, got %+v", got)
	}
	if *got[0].Measurement.Value != -1234.5 {
		t.Errorf("value = %v, want -1234.5", *got[0].Measurement.Value)
	}
}

// TestParserControllableWithRange parses a "controllable" line carrying the
// optional "range" object and asserts min/max/step are decoded correctly. The
// max pointer distinguishes a bounded number (max present) from an unbounded
// one (max absent — the VR920 case where nominal_max is not exposed).
func TestParserControllableWithRange(t *testing.T) {
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	in := `{"kind":"controllable","ski":"` + ski + `","entity":"1.1","usecase":"lpc","component":"number","unit":"W","range":{"min":0,"max":8000,"step":1},"actions":["set","clear"],"state":"1500"}`
	p := NewParser(strings.NewReader(in), fakeLogger{})

	var got []Event
	if err := p.Stream(func(ev Event) { got = append(got, ev) }); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(got) != 1 || got[0].Controllable == nil {
		t.Fatalf("expected 1 controllable event, got %+v", got)
	}
	c := got[0].Controllable
	if c.Range == nil {
		t.Fatal("range not parsed")
	}
	if c.Range.Min != 0 || c.Range.Step != 1 {
		t.Errorf("range min/step = %v/%v, want 0/1", c.Range.Min, c.Range.Step)
	}
	if c.Range.Max == nil || *c.Range.Max != 8000 {
		t.Errorf("range max = %v, want 8000", c.Range.Max)
	}
}

// TestParserControllableWithoutRange asserts the legacy wire format (no
// "range" field) still parses, with Range left nil — the bridge then publishes
// an unbounded number. Guards backward compatibility with older eebusd builds.
func TestParserControllableWithoutRange(t *testing.T) {
	ski := "aaaabbbbccccddddeeee00001111222233334444"
	in := `{"kind":"controllable","ski":"` + ski + `","entity":"1.1","usecase":"lpc","component":"number","unit":"W","actions":["set"],"state":"1500"}`
	p := NewParser(strings.NewReader(in), fakeLogger{})

	var got []Event
	if err := p.Stream(func(ev Event) { got = append(got, ev) }); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(got) != 1 || got[0].Controllable == nil {
		t.Fatalf("expected 1 controllable event, got %+v", got)
	}
	if got[0].Controllable.Range != nil {
		t.Errorf("range = %v, want nil when field absent", got[0].Controllable.Range)
	}
}
