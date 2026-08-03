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

// Kind constants mirror eebusd/internal/scanner/export.go and
// eebusd/internal/writes/dispatch.go. They are duplicated on purpose: this
// module has zero import dependency on eebusd.
const (
	KindDevice        = "device"
	KindManufacturer  = "manufacturer"
	KindConfiguration = "configuration"
	KindMeasurement   = "measurement"
	KindDiagnosis     = "diagnosis"
	// Write-channel kinds (added in 0.4.0-dev). "command" is OUTBOUND
	// (bridge → eebusd stdin); "controllable" and "command_result" are
	// INBOUND (eebusd stdout → bridge). The parser handles the inbound ones.
	KindCommand       = "command"
	KindControllable  = "controllable"
	KindCommandResult = "command_result"
	// Use-case read-signal kind (added in 0.6.0-dev). INBOUND: a use case
	// pushes one of its read values (power estimate, pausable, start time, …)
	// so the bridge can expose it as a sensor attached to the same device as
	// the control entity. Read-only: carries no command surface.
	KindUcSignal = "uc_signal"
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
	ID        string `json:"id"`
	Type      string `json:"type,omitempty"`
	Commodity string `json:"commodity,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Unit      string `json:"unit,omitempty"`
	// Value is a pointer so we can distinguish a legitimately absent value
	// (nil → drop the event) from a real measurement equal to zero (non-nil →
	// publish 0). Previously this was a plain float64 with omitempty, which
	// made a real 0 indistinguishable from "no value" and silently dropped
	// legitimate zero measurements (idle power, empty counter, 0°C, …).
	Value *float64 `json:"value"`
	Scale int      `json:"scale,omitempty"`
}

type Diagnosis struct {
	Line
	OperatingState string `json:"operating_state,omitempty"`
	LastErrorCode  string `json:"last_error_code,omitempty"`
	UpTime         string `json:"up_time,omitempty"`
}

// NumberRange is the optional input range for a number-like control entity
// (LPC power limit, …). It mirrors wucapi.NumberRange on the wire; duplicated
// here so the bridge keeps zero import dependency on eebusd (same rationale as
// the other kind structs). Max is a pointer so it can be absent (unbounded
// number) while Min/Step stay present: when Max is nil the HA number entity is
// published without a max, letting the user enter any value.
type NumberRange struct {
	Min  float64  `json:"min"`
	Max  *float64 `json:"max,omitempty"`
	Step float64  `json:"step"`
}

// Controllable is an INBOUND line (eebusd → bridge) announcing that a remote
// entity accepts one or more write actions for a given use case. The bridge
// uses it to create the matching HA control entity/entities (buttons/number/
// switch/...). Component is the HA discovery component to build ("buttons",
// "number", …), declared by the use case itself so the bridge stays agnostic.
// Unit is the Home Assistant unit of measurement for number-like components
// ("W", "A", …); empty for buttons/switch/select. Range carries the optional
// min/max/step for number components (nil for buttons/switch/select or when
// the device does not advertise a ceiling).
type Controllable struct {
	Line
	EntityType string       `json:"entity_type,omitempty"`
	UseCase    string       `json:"usecase"`
	Component  string       `json:"component"`
	Unit       string       `json:"unit,omitempty"`
	Range      *NumberRange `json:"range,omitempty"`
	Actions    []string     `json:"actions"`
	State      string       `json:"state,omitempty"`
}

// CommandResult is an INBOUND line (eebusd → bridge) reporting the outcome of
// a previously-dispatched command. MsgCounter is the SPINE message counter
// (present on success), Error carries a short reason on failure. The bridge
// logs it and may surface it on a diagnostic topic.
type CommandResult struct {
	Line
	Op         string  `json:"op"`
	Status     string  `json:"status"` // "ok" | "error"
	MsgCounter *uint32 `json:"msg_counter,omitempty"`
	Error      string  `json:"error,omitempty"`
}

// Command is an OUTBOUND line (bridge → eebusd stdin) requesting a write
// operation. It is NOT part of the parser's Event union — it is serialized by
// the bridge when an HA command is received, and written to eebusd's stdin.
type Command struct {
	Kind   string  `json:"kind"` // always "command"
	Op     string  `json:"op"`   // "<uc>.<action>"
	SKI    string  `json:"ski"`
	Entity string  `json:"entity"`
	Value  float64 `json:"value,omitempty"`
	Unit   string  `json:"unit,omitempty"`
}

// UcSignal is an INBOUND line (eebusd → bridge) carrying one read-signal value
// for a use case on a remote entity (e.g. OHPCF requested power estimate). The
// bridge exposes it as a sensor (or binary_sensor for booleans) attached to the
// same HA device + entity as the control entity, identified by (SKI, Entity,
// UseCase, Signal). Value is a typed string; ValueType selects the HA device
// class / rendering. Unit is optional ("W", "seconds", …). Read-only: there is
// no command_topic, so this kind never triggers a write to eebusd.
type UcSignal struct {
	Line
	UseCase   string `json:"usecase"`
	Signal    string `json:"signal"`
	Value     string `json:"value"`
	ValueType string `json:"value_type,omitempty"` // number|boolean|date_time|duration
	Unit      string `json:"unit,omitempty"`
}

// Event is the discriminated union returned by the parser. Exactly one field
// is non-nil per event. Consumers type-switch on it.
type Event struct {
	Device        *Device
	Manufacturer  *Manufacturer
	Configuration *Configuration
	Measurement   *Measurement
	Diagnosis     *Diagnosis
	Controllable  *Controllable
	CommandResult *CommandResult
	UcSignal      *UcSignal
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
		// value is a pointer: nil means either the field was absent or it was
		// JSON null. Both cases are "no real measurement" → drop the event
		// (HA cannot do anything useful with a sensor that has no value, and
		// rendering 0 would be wrong: it is a fake 0, not a measurement).
		// A non-nil pointer — including one that points at 0 — is a real value.
		if m.Value == nil {
			p.logger.Debug("ndjson: measurement with no value, skipping",
				"id", m.ID, "type", m.Type)
			return Event{}, false
		}
		return Event{Measurement: &m}, true

	case KindDiagnosis:
		var d Diagnosis
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			p.logger.Warn("ndjson: bad diagnosis line", "err", err.Error())
			return Event{}, false
		}
		return Event{Diagnosis: &d}, true

	case KindControllable:
		var c Controllable
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			p.logger.Warn("ndjson: bad controllable line", "err", err.Error())
			return Event{}, false
		}
		return Event{Controllable: &c}, true

	case KindCommandResult:
		var cr CommandResult
		if err := json.Unmarshal([]byte(line), &cr); err != nil {
			p.logger.Warn("ndjson: bad command_result line", "err", err.Error())
			return Event{}, false
		}
		return Event{CommandResult: &cr}, true

	case KindUcSignal:
		var us UcSignal
		if err := json.Unmarshal([]byte(line), &us); err != nil {
			p.logger.Warn("ndjson: bad uc_signal line", "err", err.Error())
			return Event{}, false
		}
		return Event{UcSignal: &us}, true

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
