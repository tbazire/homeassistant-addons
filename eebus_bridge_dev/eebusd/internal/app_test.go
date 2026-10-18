// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire

package internal

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"eebusd/internal/scanner"
	"eebusd/internal/writes/wucapi"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
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

// ---------------------------------------------------------------------------
// Poller registry + SHIP reconnection rebinding (0.11.0-dev).
//
// Regression guards for the frozen-entities bug: on every SHIP reconnection
// spine-go creates a fresh remote device generation and re-fires
// EntityChange+Add for every entity. The scanner tracker and the per-entity
// pollers must follow the new generation — never keep serving the dead one
// (frozen HA entities), never leak one poller per entity per reconnection.
// ---------------------------------------------------------------------------

// Pointer-based fakes: two equal-looking values would compare equal as
// interfaces and defeat the generation-identity checks under test. Only the
// methods the poller/scanner paths call are implemented.
type pollerTestDevice struct {
	spineapi.DeviceRemoteInterface
	ski string
}

func (f *pollerTestDevice) Ski() string { return f.ski }

type pollerTestEntity struct {
	spineapi.EntityRemoteInterface
	addr *model.EntityAddressType
}

func (f *pollerTestEntity) Address() *model.EntityAddressType { return f.addr }

func (f *pollerTestEntity) EntityType() model.EntityTypeType { return model.EntityTypeTypeCEM }

// newPollerTestApp builds an App wired for poller/reconnection tests: real
// scanner (nil local entity — helper constructors fail cleanly, no network),
// NDJSON sink discarded, quiet AppLog, one-minute poll cadence (never fires
// during a test run).
func newPollerTestApp(t *testing.T) *App {
	t.Helper()
	InitAppLog(LogLevelError, io.Discard)
	sc := scanner.NewScanner(nil, scanner.Options{})
	sc.SetDataOut(io.Discard)
	return &App{
		cfg:     &Config{PollInterval: time.Minute},
		pollCtx: context.Background(),
		pollers: make(map[string]*entityPoller),
		scanner: sc,
	}
}

// registeredPoller returns the poller handle for (ski, addr), or nil.
func registeredPoller(a *App, ski, addr string) *entityPoller {
	a.pollersMu.Lock()
	defer a.pollersMu.Unlock()
	return a.pollers[ski+"/"+addr]
}

func TestStartEntityPollerReplacesPreviousGeneration(t *testing.T) {
	a := newPollerTestApp(t)
	dev1 := &pollerTestDevice{ski: "ee55"}
	dev2 := &pollerTestDevice{ski: "ee55"}
	ent := &pollerTestEntity{addr: &model.EntityAddressType{Entity: []model.AddressEntityType{3, 1}}}

	a.startEntityPoller(dev1, ent, "ee55", "3.1")
	h1 := registeredPoller(a, "ee55", "3.1")
	if h1 == nil {
		t.Fatal("no poller registered after startEntityPoller")
	}
	if h1.device != dev1 {
		t.Error("poller must remember the device generation it was started for")
	}
	if h1.ctx.Err() != nil {
		t.Error("a freshly started poller must not be cancelled")
	}

	// The reconnection path: same (ski, entity), new generation.
	a.startEntityPoller(dev2, ent, "ee55", "3.1")
	h2 := registeredPoller(a, "ee55", "3.1")
	if h2 == nil || h2 == h1 {
		t.Fatal("the new generation's poller must replace the previous one")
	}
	if h1.ctx.Err() == nil {
		t.Error("the superseded poller must be cancelled")
	}
	if h2.ctx.Err() != nil {
		t.Error("the replacement poller must be running")
	}
}

func TestStopEntityPollersForDeviceOnlyCancelsMatchingGeneration(t *testing.T) {
	a := newPollerTestApp(t)
	dev1 := &pollerTestDevice{ski: "ff66"}
	dev2 := &pollerTestDevice{ski: "ff66"}
	other := &pollerTestDevice{ski: "aa00"}
	ent31 := &pollerTestEntity{addr: &model.EntityAddressType{Entity: []model.AddressEntityType{3, 1}}}
	ent32 := &pollerTestEntity{addr: &model.EntityAddressType{Entity: []model.AddressEntityType{3, 2}}}

	a.startEntityPoller(dev2, ent31, "ff66", "3.1")
	a.startEntityPoller(dev2, ent32, "ff66", "3.2")
	a.startEntityPoller(other, ent31, "aa00", "3.1")

	// A disconnect of the OLD generation must not touch live pollers —
	// application events are asynchronous, a reconnect may already have
	// started new-generation pollers.
	a.stopEntityPollersForDevice("ff66", dev1)
	for _, key := range []struct{ ski, addr string }{{"ff66", "3.1"}, {"ff66", "3.2"}, {"aa00", "3.1"}} {
		if h := registeredPoller(a, key.ski, key.addr); h == nil || h.ctx.Err() != nil {
			t.Errorf("poller %s/%s must survive a stop for a non-matching generation", key.ski, key.addr)
		}
	}

	// Stopping the current generation cancels exactly its pollers.
	a.stopEntityPollersForDevice("ff66", dev2)
	for _, key := range []struct{ ski, addr string }{{"ff66", "3.1"}, {"ff66", "3.2"}} {
		if h := registeredPoller(a, key.ski, key.addr); h != nil {
			t.Errorf("poller %s/%s must be unregistered after its generation stopped", key.ski, key.addr)
		}
	}
	if h := registeredPoller(a, "aa00", "3.1"); h == nil || h.ctx.Err() != nil {
		t.Error("pollers of another SKI must be untouched")
	}
}

func TestHandleEventDeviceRemoveDropsGenerationState(t *testing.T) {
	a := newPollerTestApp(t)
	dev1 := &pollerTestDevice{ski: "ab77"}
	dev2 := &pollerTestDevice{ski: "ab77"}
	ent := &pollerTestEntity{addr: &model.EntityAddressType{Entity: []model.AddressEntityType{3, 1}}}

	a.startEntityPoller(dev1, ent, "ab77", "3.1")
	h1 := registeredPoller(a, "ab77", "3.1")
	a.scanner.ScanEntity(dev1, ent) // seeds a tracker bound to the dead generation

	// spine-go reports the disconnection: DeviceChange+Remove with the dead
	// device in the payload.
	a.HandleEvent(spineapi.EventPayload{
		Ski:        "ab77",
		EventType:  spineapi.EventTypeDeviceChange,
		ChangeType: spineapi.ElementChangeRemove,
		Device:     dev1,
	})

	if h := registeredPoller(a, "ab77", "3.1"); h != nil {
		t.Error("the dead generation's poller must be unregistered by the Remove event")
	}
	if h1.ctx.Err() == nil {
		t.Error("the dead generation's poller must be cancelled by the Remove event")
	}
	if a.scanner.DropDeviceIfStale("ab77", dev2) {
		t.Error("no scan state may remain for the dead generation after the Remove event")
	}

	// The reconnection: a new-generation poller is live when the late Remove
	// event of the OLD connection finally runs — it must survive untouched.
	a.startEntityPoller(dev2, ent, "ab77", "3.1")
	h2 := registeredPoller(a, "ab77", "3.1")
	if h2 == nil {
		t.Fatal("new-generation poller not registered")
	}
	a.HandleEvent(spineapi.EventPayload{
		Ski:        "ab77",
		EventType:  spineapi.EventTypeDeviceChange,
		ChangeType: spineapi.ElementChangeRemove,
		Device:     dev1,
	})
	if h := registeredPoller(a, "ab77", "3.1"); h == nil || h.ctx.Err() != nil {
		t.Error("a late Remove of the old generation must not cancel the new generation's poller")
	}

	// DeviceChange+Add (detailed discovery of the new generation) is not a
	// disconnect: it must not purge anything either.
	a.HandleEvent(spineapi.EventPayload{
		Ski:        "ab77",
		EventType:  spineapi.EventTypeDeviceChange,
		ChangeType: spineapi.ElementChangeAdd,
		Device:     dev2,
	})
	if h := registeredPoller(a, "ab77", "3.1"); h == nil || h.ctx.Err() != nil {
		t.Error("DeviceChange+Add must not cancel live pollers")
	}
}
