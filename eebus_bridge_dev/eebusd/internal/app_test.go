// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire

package internal

import (
	"bytes"
	"strings"
	"sync"
	"testing"
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
