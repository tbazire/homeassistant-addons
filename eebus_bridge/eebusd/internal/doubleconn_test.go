// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Tests for the SHIP double-connection loop detector (issue #57).
//
// Some devices keep a wedged stale SHIP session and reject every
// reconnection attempt with the websocket close code 4201 ("Double
// connection") — a loop only a session reset (add-on restart) ever cured.
// The detector counts consecutive 4201 pairing errors per SKI and, once the
// loop signature is reached, triggers the daemon self-restart instead of
// waiting for a human.
//
// The tests drive ServicePairingDetailUpdate exactly the way eebus-go
// delivers pairing errors, and observe the decision through an injected
// selfRestart hook (the production hook kills the process).

package internal

import (
	"strings"
	"testing"
	"time"

	shipapi "github.com/enbility/ship-go/api"
	"github.com/gorilla/websocket"
)

// doubleConnTestSki is the SKI used across these tests.
const doubleConnTestSki = "aa11bb22cc33dd44"

// doubleConnError builds the error eebus-go surfaces when the remote closes
// the websocket with the SHIP double-connection close code.
func doubleConnError() error {
	return &websocket.CloseError{Code: shipCloseCodeDoubleConnection, Text: "Double connection"}
}

// otherPairingError builds a non-4201 pairing failure.
func otherPairingError() error {
	return &websocket.CloseError{Code: websocket.CloseGoingAway, Text: "going away"}
}

// pairingDetail wraps an error the way the pairing state machine reports it.
func pairingDetail(err error) *shipapi.ConnectionStateDetail {
	return shipapi.NewConnectionStateDetail(shipapi.ConnectionStateError, err)
}

// pairingIdentity builds the identity eebus-go carries on pairing events.
func pairingIdentity(ski string) shipapi.ServiceIdentity {
	return shipapi.ServiceIdentity{SKI: ski}
}

// doubleConnTestApp wires the poller-test app with a selfRestart recorder.
// restarts collects the reasons the detector passed to the hook.
func doubleConnTestApp(t *testing.T) (*App, *[]string) {
	t.Helper()
	a := newPollerTestApp(t)
	restarts := &[]string{}
	a.selfRestart = func(reason string) {
		*restarts = append(*restarts, reason)
	}
	return a, restarts
}

// doubleConnCount returns the current tracker count for a ski (0 if none).
func doubleConnCount(a *App, ski string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.doubleConnLoops[ski]
	if st == nil {
		return 0
	}
	return st.count
}

// rewindDoubleConnClock pretends the last 4201 for a ski happened long ago,
// to test the window expiry without sleeping.
func rewindDoubleConnClock(a *App, ski string, d time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if st := a.doubleConnLoops[ski]; st != nil {
		st.lastAt = st.lastAt.Add(-d)
	}
}

// TestDoubleConnectionLoopTriggersSelfRestart pins the core behaviour: two
// close-4201 rejections within the window restart the daemon exactly once,
// with a reason that names the SKI and the loop. This is the automated
// replacement for the manual add-on restart the Buderus MX400 report (#57)
// needed.
func TestDoubleConnectionLoopTriggersSelfRestart(t *testing.T) {
	a, restarts := doubleConnTestApp(t)

	a.ServicePairingDetailUpdate(pairingIdentity(doubleConnTestSki), pairingDetail(doubleConnError()))
	a.ServicePairingDetailUpdate(pairingIdentity(doubleConnTestSki), pairingDetail(doubleConnError()))

	if len(*restarts) != 1 {
		t.Fatalf("selfRestart calls = %d, want 1 (reasons: %v)", len(*restarts), *restarts)
	}
	if !strings.Contains((*restarts)[0], doubleConnTestSki) || !strings.Contains((*restarts)[0], "4201") {
		t.Errorf("restart reason does not name the SKI and the close code: %q", (*restarts)[0])
	}

	// Further 4201s during the shutdown must not re-trigger the restart.
	a.ServicePairingDetailUpdate(pairingIdentity(doubleConnTestSki), pairingDetail(doubleConnError()))
	if len(*restarts) != 1 {
		t.Fatalf("selfRestart fired %d times, want exactly 1", len(*restarts))
	}
}

// TestSingleDoubleConnectionCloseDoesNotRestart pins that one 4201 is only
// logged and counted: a transient double connection (both sides racing, the
// remote's SKI comparison resolving it) must not restart the daemon.
func TestSingleDoubleConnectionCloseDoesNotRestart(t *testing.T) {
	a, restarts := doubleConnTestApp(t)

	a.ServicePairingDetailUpdate(pairingIdentity(doubleConnTestSki), pairingDetail(doubleConnError()))

	if len(*restarts) != 0 {
		t.Fatalf("selfRestart fired after a single 4201: %v", *restarts)
	}
	if got := doubleConnCount(a, doubleConnTestSki); got != 1 {
		t.Errorf("tracker count = %d, want 1", got)
	}
}

// TestDoubleConnectionCounterResetsAfterWindow pins that rejections further
// apart than the window are independent incidents: the second one starts a
// fresh count and must not reach the threshold.
func TestDoubleConnectionCounterResetsAfterWindow(t *testing.T) {
	a, restarts := doubleConnTestApp(t)

	a.ServicePairingDetailUpdate(pairingIdentity(doubleConnTestSki), pairingDetail(doubleConnError()))
	rewindDoubleConnClock(a, doubleConnTestSki, doubleConnLoopWindow+time.Minute)
	a.ServicePairingDetailUpdate(pairingIdentity(doubleConnTestSki), pairingDetail(doubleConnError()))

	if len(*restarts) != 0 {
		t.Fatalf("selfRestart fired across the window boundary: %v", *restarts)
	}
	if got := doubleConnCount(a, doubleConnTestSki); got != 1 {
		t.Errorf("count after window expiry = %d, want 1 (fresh incident)", got)
	}
}

// TestDoubleConnectionCounterResetsOnPairingCompleted pins that a successful
// pairing clears the tracker: an incident resolved by the device must not
// make the next (unrelated) 4201 trigger a restart.
func TestDoubleConnectionCounterResetsOnPairingCompleted(t *testing.T) {
	a, restarts := doubleConnTestApp(t)

	a.ServicePairingDetailUpdate(pairingIdentity(doubleConnTestSki), pairingDetail(doubleConnError()))
	a.ServicePairingDetailUpdate(pairingIdentity(doubleConnTestSki),
		shipapi.NewConnectionStateDetail(shipapi.ConnectionStateCompleted, nil))
	a.ServicePairingDetailUpdate(pairingIdentity(doubleConnTestSki), pairingDetail(doubleConnError()))

	if len(*restarts) != 0 {
		t.Fatalf("selfRestart fired despite the successful pairing in between: %v", *restarts)
	}
	if got := doubleConnCount(a, doubleConnTestSki); got != 1 {
		t.Errorf("count after reset = %d, want 1", got)
	}
}

// TestOtherPairingErrorsDoNotRestart pins that only close-4201 rejections
// feed the tracker: ordinary pairing failures (transport, timeouts, …) have
// their own retry paths and must never reach the restart decision.
func TestOtherPairingErrorsDoNotRestart(t *testing.T) {
	a, restarts := doubleConnTestApp(t)

	a.ServicePairingDetailUpdate(pairingIdentity(doubleConnTestSki), pairingDetail(otherPairingError()))
	a.ServicePairingDetailUpdate(pairingIdentity(doubleConnTestSki), pairingDetail(otherPairingError()))

	if len(*restarts) != 0 {
		t.Fatalf("selfRestart fired on non-4201 errors: %v", *restarts)
	}
	if got := doubleConnCount(a, doubleConnTestSki); got != 0 {
		t.Errorf("tracker count = %d, want 0 (only 4201s are counted)", got)
	}
}
