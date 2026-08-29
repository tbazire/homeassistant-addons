// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire

package internal

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"eebusd/internal/writes/wucapi"
)

// TestWriteLineAtomicUnderConcurrency is the regression guard for the
// interleaved-stdout bug: emitSignal/emitControllable previously did two
// separate os.Stdout.Write calls (payload + "\n") with no lock, so concurrent
// SPINE-goroutine emissions could merge into "{...}{...}" lines that the
// bridge rejected as unparseable ("invalid character '{' after top-level
// value"). writeLine must now serialize every line under outMu and write it in
// a single Write call, so every line in the captured output is well-formed.
func TestWriteLineAtomicUnderConcurrency(t *testing.T) {
	a := &App{out: &bytes.Buffer{}}

	const goroutines = 32
	const linesPerGoroutine = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < linesPerGoroutine; i++ {
				// A realistic NDJSON-ish payload. The content does not matter;
				// only that each line lands whole and newline-terminated.
				a.writeLine([]byte(`{"kind":"uc_signal","signal":"x","value":"1"}`))
			}
		}()
	}
	wg.Wait()

	out := a.out.(*bytes.Buffer).String()
	total := goroutines * linesPerGoroutine
	got := 0
	for _, ln := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		got++
		// Every line must start with '{' and end with '}' (no half-written or
		// merged payloads). A merged line would contain "}{" in the middle.
		if !strings.HasPrefix(ln, "{") || !strings.HasSuffix(ln, "}") {
			t.Fatalf("corrupt line %d (len=%d): %q", got, len(ln), trunc(ln, 80))
		}
		if strings.Contains(ln, "}{") {
			t.Fatalf("line %d looks interleaved: %q", got, trunc(ln, 80))
		}
	}
	if got != total {
		t.Errorf("line count = %d, want %d (some lines were lost or merged)", got, total)
	}
}

// TestWriteLineSingleCallPerLine asserts the single-Write-call guarantee at
// the writer level: a counting writer must observe exactly one Write per line
// (payload+newline together). Two writes per line is the original bug.
func TestWriteLineSingleCallPerLine(t *testing.T) {
	cw := &countingWriter{}
	a := &App{out: cw}
	a.writeLine([]byte(`{"k":"a"}`))
	a.writeLine([]byte(`{"k":"b"}`))
	if cw.calls != 2 {
		t.Errorf("write calls = %d, want 2 (one per line)", cw.calls)
	}
	if cw.buf.String() != "{\"k\":\"a\"}\n{\"k\":\"b\"}\n" {
		t.Errorf("output = %q", cw.buf.String())
	}
}

type countingWriter struct {
	buf   bytes.Buffer
	calls int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.calls++
	return c.buf.Write(p)
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// TestApplyNumberRangeFallback_NumberNilAppliesDefault is the regression guard
// for the "slider capped at 100 W" bug: when a number use case returns no
// range (device did not expose nominal_max), the daemon MUST still publish a
// realistic max, otherwise Home Assistant applies its hard-coded default of
// 100. The fallback uses EffectiveLPCMaxLimit() (configurable, default 25000).
func TestApplyNumberRangeFallback_NumberNilAppliesDefault(t *testing.T) {
	a := &App{cfg: &Config{}} // LPCMaxLimitW=0 → EffectiveLPCMaxLimit=25000
	rng := a.applyNumberRangeFallback("number", nil)
	if rng == nil {
		t.Fatal("expected a fallback range for number, got nil")
	}
	if !rng.HasMax {
		t.Error("fallback range should have HasMax=true")
	}
	if rng.Max != DefaultLPCMaxLimitW {
		t.Errorf("fallback Max = %v, want default %v", rng.Max, DefaultLPCMaxLimitW)
	}
	if rng.Min != 0 || rng.Step != 1 {
		t.Errorf("fallback Min/Step = %v/%v, want 0/1", rng.Min, rng.Step)
	}
}

// TestApplyNumberRangeFallback_NumberNilHonorsConfig asserts a user-configured
// max wins over the hard-coded default.
func TestApplyNumberRangeFallback_NumberNilHonorsConfig(t *testing.T) {
	a := &App{cfg: &Config{LPCMaxLimitW: 9000}}
	rng := a.applyNumberRangeFallback("number", nil)
	if rng == nil || rng.Max != 9000 {
		t.Errorf("fallback Max = %v, want 9000 (configured)", rng)
	}
}

// TestApplyNumberRangeFallback_NumberPreserved asserts a device-derived range
// is returned unchanged (the fallback never overrides a real ceiling).
func TestApplyNumberRangeFallback_NumberPreserved(t *testing.T) {
	a := &App{cfg: &Config{LPCMaxLimitW: 9000}}
	provided := &wucapi.NumberRange{Min: 0, Max: 4200, Step: 1, HasMax: true}
	rng := a.applyNumberRangeFallback("number", provided)
	if rng != provided {
		t.Errorf("device-derived range was replaced: got %v, want %v", rng, provided)
	}
	if rng.Max != 4200 {
		t.Errorf("Max = %v, want 4200 (device value preserved)", rng.Max)
	}
}

// TestApplyNumberRangeFallback_ClimateNilReturnsNil asserts climate/switch/
// select components never get a fallback range (they are not numeric sliders).
func TestApplyNumberRangeFallback_ClimateNilReturnsNil(t *testing.T) {
	a := &App{cfg: &Config{}}
	if rng := a.applyNumberRangeFallback("climate", nil); rng != nil {
		t.Errorf("climate fallback = %v, want nil", rng)
	}
}
