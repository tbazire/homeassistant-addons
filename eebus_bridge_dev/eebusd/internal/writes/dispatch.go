// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package writes: dispatch.go — routing of inbound NDJSON commands to the
// registered use cases, and emission of "command_result" lines.
//
// Wire contract (bridge → eebusd, one JSON object per line on stdin):
//
//	{"kind":"command","op":"<uc>.<action>","ski":"<40hex>","entity":"<addr>","value":<num>,"unit":"<u>"}
//
// The dispatcher:
//  1. splits op on the first "." → (useCaseName, action)
//  2. looks up the use case in the registry
//  3. resolves the remote entity from (ski, addr) via the EntityResolver
//  4. asks the use case to Dispatch, wiring a ResultCB that writes a
//     "command_result" line on the provided writer.
//
// On success the dispatcher returns nil synchronously; the SPINE write result
// itself arrives asynchronously and flows through resultCB. On any synchronous
// failure (unknown op, missing use case, unknown entity) it writes a
// command_result with status=error and returns the error so the caller can log
// it (the line is still emitted so the bridge can surface it to HA).

package writes

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"eebusd/internal/writes/wucapi"
	spineapi "github.com/enbility/spine-go/api"
)

// EntityResolver turns a (ski, entityAddress) pair into the matching
// spineapi.EntityRemoteInterface. Implemented by *internal.App.
type EntityResolver interface {
	EntityBySkiAndAddr(ski, addr string) (spineapi.EntityRemoteInterface, error)
}

// Dispatcher routes inbound command lines to use cases and emits command_result
// lines on Out. It is safe for concurrent use: stdin is read from one goroutine,
// but the ResultCB may fire from a SPINE goroutine, so the writer is mutex-guarded.
type Dispatcher struct {
	resolver EntityResolver
	out      io.Writer

	mu sync.Mutex
}

// NewDispatcher wires the resolver and the result-line writer (typically the
// same writer the read-side scanner emits on — stdout in -json mode).
func NewDispatcher(resolver EntityResolver, out io.Writer) *Dispatcher {
	return &Dispatcher{resolver: resolver, out: out}
}

// CommandIn mirrors the bridge → eebusd wire contract. The Value/Unit pair is
// optional and only meaningful for some actions (e.g. ohpcf.schedule).
type CommandIn struct {
	Kind   string  `json:"kind"`           // always "command"
	Op     string  `json:"op"`             // "<uc>.<action>"
	SKI    string  `json:"ski"`            // remote device SKI
	Entity string  `json:"entity"`         // entity address, e.g. "3.1"
	Value  float64 `json:"value"`          // optional numeric payload
	Unit   string  `json:"unit,omitempty"` // optional unit hint
}

// CommandResult is the eebusd → bridge outcome line. msgCounter is present on
// success (SPINE message counter for correlation); error carries a short,
// non-sensitive reason on failure.
type CommandResult struct {
	Kind       string  `json:"kind"` // always "command_result"
	Op         string  `json:"op"`
	SKI        string  `json:"ski"`
	Entity     string  `json:"entity"`
	Status     string  `json:"status"`                // "ok" | "error"
	MsgCounter *uint32 `json:"msg_counter,omitempty"` // SPINE msg counter (ok only)
	Error      string  `json:"error,omitempty"`       // short reason (error only)
}

// HandleLine decodes one raw stdin line and dispatches it. Returns nil on a
// successful handoff to the use case; the asynchronous result is reported via
// the resultCB the dispatcher registers. Returns an error for synchronous
// failures (malformed JSON, unknown op, unknown entity, incompatible entity),
// after having written the matching command_result line to Out.
//
// Empty lines and non-command lines (kind != "command") are silently ignored
// (return nil) so the dispatcher is robust to eebusd ever reading stray output.
func (d *Dispatcher) HandleLine(raw []byte) error {
	line := strings.TrimSpace(string(raw))
	if line == "" {
		return nil
	}
	var cmd CommandIn
	if err := json.Unmarshal([]byte(line), &cmd); err != nil {
		// Malformed input — we cannot even route it, so no command_result.
		return fmt.Errorf("writes: unparseable command line: %w", err)
	}
	if cmd.Kind != "command" {
		// Not ours; ignore silently.
		return nil
	}
	return d.dispatch(cmd)
}

func (d *Dispatcher) dispatch(cmd CommandIn) error {
	// 1. Split op → (useCaseName, action).
	dot := strings.IndexByte(cmd.Op, '.')
	if dot < 0 {
		return d.emitError(cmd, fmt.Sprintf("malformed op %q (want <uc>.<action>)", cmd.Op))
	}
	ucName := cmd.Op[:dot]
	action := cmd.Op[dot+1:]

	uc := wucapi.Get(ucName)
	if uc == nil {
		return d.emitError(cmd, fmt.Sprintf("unknown use case %q", ucName))
	}

	// 2. Resolve the entity from (ski, addr).
	if d.resolver == nil {
		return d.emitError(cmd, "no entity resolver configured")
	}
	entity, err := d.resolver.EntityBySkiAndAddr(cmd.SKI, cmd.Entity)
	if err != nil {
		return d.emitError(cmd, fmt.Sprintf("entity lookup: %v", err))
	}
	if entity == nil {
		return d.emitError(cmd, fmt.Sprintf("entity %s not found for ski %s", cmd.Entity, maskSKI(cmd.SKI)))
	}

	// 3. Compatibility guard — never let a write hit an entity that does not
	//    advertise the use case.
	if !uc.IsCompatible(entity) {
		return d.emitError(cmd, fmt.Sprintf("entity %s is not compatible with %s", cmd.Entity, ucName))
	}

	// 4. Dispatch. The use case is responsible for invoking resultCB exactly
	//    once, from any goroutine, with the SPINE result. We wrap it to emit
	//    the command_result line.
	args := wucapi.Args{Value: cmd.Value, Unit: cmd.Unit}
	resultCB := func(status wucapi.ResultStatus, msgCounter *uint32, errStr string) {
		d.emitResult(cmd, status, msgCounter, errStr)
	}
	if err := uc.Dispatch(action, cmd.SKI, entity, args, resultCB); err != nil {
		// Synchronous failure before the SPINE write was registered — emit
		// the result line now so the bridge is never left waiting.
		d.emitResult(cmd, wucapi.ResultError, nil, err.Error())
		return fmt.Errorf("dispatch %s: %w", cmd.Op, err)
	}
	return nil
}

// emitResult writes one command_result line. Serialized against other writes.
func (d *Dispatcher) emitResult(cmd CommandIn, status wucapi.ResultStatus, msgCounter *uint32, errStr string) {
	res := CommandResult{
		Kind:       "command_result",
		Op:         cmd.Op,
		SKI:        cmd.SKI,
		Entity:     cmd.Entity,
		Status:     string(status),
		MsgCounter: msgCounter,
	}
	if status == wucapi.ResultError {
		res.Error = errStr
	}
	payload, err := json.Marshal(res)
	if err != nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, _ = fmt.Fprintln(d.out, string(payload))
}

// emitError is a convenience wrapper for the synchronous-failure case.
func (d *Dispatcher) emitError(cmd CommandIn, reason string) error {
	d.emitResult(cmd, wucapi.ResultError, nil, reason)
	return fmt.Errorf("%s", reason)
}

// maskSKI returns a redacted form of the SKI for inclusion in error messages
// going to the bridge. The SKI is already public (it is the device identifier
// on the EEBUS network) and is emitted on every read-side line too, but we
// keep only the tail here out of caution for log-aggregation contexts.
func maskSKI(ski string) string {
	if len(ski) <= 8 {
		return "…"
	}
	return "…" + ski[len(ski)-8:]
}
