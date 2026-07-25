// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package internal: ndjson.go — streaming NDJSON decoder for eebusd's stdout.
//
// Each line emitted by eebusd (in -json mode) is a self-contained JSON object
// carrying a "kind" discriminant. This file defines:
//   - one Go struct per kind;
//   - a Parser that reads lines and produces typed events.
//
// Robustness contract: a malformed or unknown line is logged and skipped — it
// must NOT abort the stream. eebusd may emit a partial line on shutdown or
// introduce new kinds the bridge does not know yet.

package internal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Kind constants mirror eebusd/internal/scanner/export.go. They are duplicated
// on purpose: this module has zero import dependency on eebusd.
const (
	KindDevice        = "device"
	KindManufacturer  = "manufacturer"
	KindConfiguration = "configuration"
	KindMeasurement   = "measurement"
	KindDiagnosis     = "diagnosis"
)

// Line is the common envelope embedded by every typed payload.
type Line struct {
	Kind   string `json:"kind"`
	SKI    string `json:"ski"`
	Entity string `json:"entity"`
	Time   string `json:"time"`
}

type Device struct {
	Line
	EntityType string `json:"entity_type"`
}

type Manufacturer struct {
	Line
	DeviceName       string `json:"device_name,omitempty"`
	DeviceCode       string `json:"device_code,omitempty"`
	SerialNumber     string `json:"serial,omitempty"`
	BrandName        string `json:"brand_name,omitempty"`
	VendorName       string `json:"vendor_name,omitempty"`
	VendorCode       string `json:"vendor_code,omitempty"`
	SoftwareRevision string `json:"sw_version,omitempty"`
	HardwareRevision string `json:"hw_version,omitempty"`
}

type Configuration struct {
	Line
	KeyID     string `json:"key_id"`
	KeyName   string `json:"key_name,omitempty"`
	Value     string `json:"value,omitempty"`
	ValueType string `json:"value_type,omitempty"`
}

type Measurement struct {
	Line
	ID        string  `json:"id"`
	Type      string  `json:"type,omitempty"`
	Commodity string  `json:"commodity,omitempty"`
	Scope     string  `json:"scope,omitempty"`
	Unit      string  `json:"unit,omitempty"`
	Value     float64 `json:"value,omitempty"`
	Scale     int     `json:"scale,omitempty"`
}

type Diagnosis struct {
	Line
	OperatingState string `json:"operating_state,omitempty"`
	LastErrorCode  string `json:"last_error_code,omitempty"`
	UpTime         string `json:"up_time,omitempty"`
}

// Event is the discriminated union returned by the parser. Exactly one field
// is non-nil per event. Consumers type-switch on it.
type Event struct {
	Device        *Device
	Manufacturer  *Manufacturer
	Configuration *Configuration
	Measurement   *Measurement
	Diagnosis     *Diagnosis
}

// Parser reads NDJSON lines from r and yields typed Events on the returned
// channel. The channel is closed when r returns io.EOF. A read error (other
// than EOF) is delivered as a final event on the error channel.
//
// Lines that fail to parse or use an unknown kind are logged via logger and
// skipped, never propagated as errors.
type Parser struct {
	r      io.Reader
	logger Logger
}

// Logger is the minimal logging surface the parser needs. Defined here so the
// parser depends on an interface, not on *slog.Logger directly.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Debug(msg string, args ...any)
}

// NewParser returns a Parser reading from r and using logger for diagnostics.
func NewParser(r io.Reader, logger Logger) *Parser {
	return &Parser{r: r, logger: logger}
}

// Stream parses until EOF and invokes handler for every recognized event.
// Malformed lines are logged and skipped. Returns the first read error (io.EOF
// excluded).
func (p *Parser) Stream(handler func(Event)) error {
	scanner := bufio.NewScanner(p.r)
	// eebusd lines are small (<1KB), but raise the limit defensively so a
	// long manufacturer description cannot trip the default 64KB cap.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		ev, ok := p.parseLine(line)
		if !ok {
			continue
		}
		handler(ev)
	}
	return scanner.Err()
}

// parseLine decodes one NDJSON line into a typed Event. Returns (zero, false)
// when the line is malformed or uses an unknown kind.
func (p *Parser) parseLine(line string) (Event, bool) {
	var head Line
	if err := json.Unmarshal([]byte(line), &head); err != nil {
		p.logger.Warn("ndjson: skipping unparseable line", "err", err.Error(), "line", truncate(line))
		return Event{}, false
	}
	if head.Kind == "" {
		p.logger.Warn("ndjson: line missing kind", "line", truncate(line))
		return Event{}, false
	}

	switch head.Kind {
	case KindDevice:
		var d Device
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			p.logger.Warn("ndjson: bad device line", "err", err.Error())
			return Event{}, false
		}
		return Event{Device: &d}, true

	case KindManufacturer:
		var m Manufacturer
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			p.logger.Warn("ndjson: bad manufacturer line", "err", err.Error())
			return Event{}, false
		}
		return Event{Manufacturer: &m}, true

	case KindConfiguration:
		var c Configuration
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			p.logger.Warn("ndjson: bad configuration line", "err", err.Error())
			return Event{}, false
		}
		return Event{Configuration: &c}, true

	case KindMeasurement:
		var m Measurement
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			p.logger.Warn("ndjson: bad measurement line", "err", err.Error())
			return Event{}, false
		}
		// Check if value is explicitly null or missing (should be a valid number)
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err == nil {
			if val, ok := raw["value"]; ok {
				if val == nil {
					p.logger.Warn("ndjson: measurement with null value", "id", m.ID, "type", m.Type)
					return Event{}, false
				}
			} else {
				p.logger.Warn("ndjson: measurement with missing value field", "id", m.ID, "type", m.Type)
				return Event{}, false
			}
		}
		return Event{Measurement: &m}, true

	case KindDiagnosis:
		var d Diagnosis
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			p.logger.Warn("ndjson: bad diagnosis line", "err", err.Error())
			return Event{}, false
		}
		return Event{Diagnosis: &d}, true

	default:
		p.logger.Debug("ndjson: unknown kind, ignoring", "kind", head.Kind)
		return Event{}, false
	}
}

func truncate(s string) string {
	const max = 120
	if len(s) <= max {
		return s
	}
	return fmt.Sprintf("%s…", s[:max])
}
