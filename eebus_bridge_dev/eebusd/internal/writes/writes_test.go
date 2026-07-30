// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire

package writes

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	spineapi "github.com/enbility/spine-go/api"
)

// The dispatcher depends on EntityRemoteInterface (a large interface from
// spine-go). Rather than hand-stub 20+ methods, we test the dispatcher's
// routing logic through the error paths that do NOT require a usable entity:
//   - unknown use case
//   - malformed op
//   - non-command / empty lines ignored
//   - entity lookup failure (resolver returns err)
//
// The end-to-end happy path (entity resolved, IsCompatible true, Dispatch
// called) is exercised in the ohpcf package test via a real bind against the
// testhelper harness used by eebus-go's own use case tests.

type mockResolver struct {
	err error
}

func (m *mockResolver) EntityBySkiAndAddr(ski, addr string) (spineapi.EntityRemoteInterface, error) {
	return nil, m.err
}

func TestHandleLine_UnknownUseCase(t *testing.T) {
	var buf bytes.Buffer
	d := NewDispatcher(&mockResolver{}, &buf)

	cmd := `{"kind":"command","op":"doesnotexist.pause","ski":"a","entity":"0"}`
	if err := d.HandleLine([]byte(cmd)); err == nil {
		t.Fatal("expected error for unknown use case")
	}
	out := buf.String()
	if !strings.Contains(out, `"status":"error"`) {
		t.Errorf("expected command_result error line, got: %s", out)
	}
	if !strings.Contains(out, "unknown use case") {
		t.Errorf("error result should mention unknown use case, got: %s", out)
	}
}

func TestHandleLine_MalformedOp(t *testing.T) {
	var buf bytes.Buffer
	d := NewDispatcher(&mockResolver{}, &buf)
	cmd := `{"kind":"command","op":"noprefix","ski":"a","entity":"0"}`
	if err := d.HandleLine([]byte(cmd)); err == nil {
		t.Fatal("expected error for op without dot")
	}
	if !strings.Contains(buf.String(), "malformed op") {
		t.Errorf("expected malformed op error, got: %s", buf.String())
	}
}

func TestHandleLine_IgnoresNonCommandLines(t *testing.T) {
	var buf bytes.Buffer
	d := NewDispatcher(&mockResolver{}, &buf)
	// A measurement line must be silently ignored.
	if err := d.HandleLine([]byte(`{"kind":"measurement","id":"x"}`)); err != nil {
		t.Errorf("non-command line should be ignored, got err: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("no output expected for ignored line, got: %s", buf.String())
	}
}

func TestHandleLine_EmptyLine(t *testing.T) {
	var buf bytes.Buffer
	d := NewDispatcher(&mockResolver{}, &buf)
	if err := d.HandleLine([]byte("   ")); err != nil {
		t.Errorf("empty line should be ignored, got err: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("no output expected for empty line, got: %s", buf.String())
	}
}

func TestHandleLine_UnparseableJSON(t *testing.T) {
	var buf bytes.Buffer
	d := NewDispatcher(&mockResolver{}, &buf)
	// Garbage that is not valid JSON.
	if err := d.HandleLine([]byte("{not json")); err == nil {
		t.Fatal("expected error for unparseable JSON")
	}
	// No command_result emitted: we don't know which command to attribute it to.
	if buf.Len() != 0 {
		t.Errorf("no output expected for unparseable line, got: %s", buf.String())
	}
}

func TestHandleLine_EntityLookupFails(t *testing.T) {
	var buf bytes.Buffer
	d := NewDispatcher(&mockResolver{err: errors.New("no such ski")}, &buf)

	cmd := `{"kind":"command","op":"ohpcf.pause","ski":"a","entity":"0"}`
	if err := d.HandleLine([]byte(cmd)); err == nil {
		t.Fatal("expected error when entity lookup fails")
	}
	out := buf.String()
	if !strings.Contains(out, `"status":"error"`) {
		t.Errorf("expected error result, got: %s", out)
	}
	if !strings.Contains(out, "entity lookup") {
		t.Errorf("error should mention entity lookup, got: %s", out)
	}
}

func TestHandleLine_NilResolver(t *testing.T) {
	// Defensive: if no resolver is wired at all, we must still emit a clean
	// error result instead of panicking on a nil pointer.
	var buf bytes.Buffer
	d := NewDispatcher(nil, &buf)
	cmd := `{"kind":"command","op":"ohpcf.pause","ski":"a","entity":"0"}`
	if err := d.HandleLine([]byte(cmd)); err == nil {
		t.Fatal("expected error when resolver is nil")
	}
	if !strings.Contains(buf.String(), "no entity resolver") {
		t.Errorf("expected 'no entity resolver' error, got: %s", buf.String())
	}
}

func TestCommandResultJSONShape(t *testing.T) {
	// Verify the wire shape so the bridge parser's struct tags match exactly.
	mc := uint32(7)
	res := CommandResult{
		Kind: "command_result", Op: "ohpcf.pause", SKI: "s", Entity: "3.1",
		Status: "ok", MsgCounter: &mc,
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, want := range []string{`"kind":"command_result"`, `"op":"ohpcf.pause"`, `"status":"ok"`, `"msg_counter":7`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
	// Error variant must not carry msg_counter.
	resErr := CommandResult{
		Kind: "command_result", Op: "x.y", SKI: "s", Entity: "0",
		Status: "error", Error: "boom",
	}
	b2, _ := json.Marshal(resErr)
	if strings.Contains(string(b2), "msg_counter") {
		t.Errorf("error result must omit msg_counter, got: %s", b2)
	}
}

func TestMaskSKI(t *testing.T) {
	// maskSKI is used in error/result lines; it must keep only the tail so the
	// full SKI does not leak in aggregated logs.
	full := "0123456789abcdef0123456789abcdef01234567"
	got := maskSKI(full)
	if got == full {
		t.Errorf("maskSKI returned the full SKI: %s", got)
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("maskSKI should start with …, got: %s", got)
	}
}

func TestReExports(t *testing.T) {
	// All/Names/Get are thin re-exports of wucapi; make sure they return the
	// use cases shipped in this build (ohpcf + lpc, wired via writes/bind.go's
	// blank imports).
	if len(All()) == 0 {
		t.Fatal("All() is empty — no use case registered")
	}
	for _, want := range []string{"ohpcf", "lpc"} {
		if Get(want) == nil {
			t.Errorf("Get(%s) returned nil — module not registered", want)
		}
		if !contains(Names(), want) {
			t.Errorf("Names() should contain %s, got %v", want, Names())
		}
	}
	// Each shipped module declares its HA component + unit; assert the
	// contract so a silent rename breaks the test instead of HA discovery.
	if uc := Get("ohpcf"); uc != nil {
		if uc.HAComponent() != "climate" {
			t.Errorf("ohpcf.HAComponent = %q, want climate", uc.HAComponent())
		}
	}
	if uc := Get("lpc"); uc != nil {
		if uc.HAComponent() != "number" {
			t.Errorf("lpc.HAComponent = %q, want number", uc.HAComponent())
		}
		if uc.HAUnit() != "W" {
			t.Errorf("lpc.HAUnit = %q, want W", uc.HAUnit())
		}
	}
}

func contains(slice []string, s string) bool {
	for _, x := range slice {
		if x == s {
			return true
		}
	}
	return false
}
