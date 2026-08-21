// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package internal: mqtt_test.go — MQTT traffic logging.
//
// The bridge logs every outgoing publish, every subscription and every
// incoming message at debug level, so an operator with log_level=debug|trace
// can see the exact broker traffic ("entities don't update" on an external
// broker). These tests pin that behaviour without a broker: paho fails fast
// with ErrNotConnected on a never-connected client, so the calls return
// immediately and only the log lines are asserted.

package internal

import (
	"fmt"
	"strings"
	"testing"
)

// recordingLogger captures Debug lines as "msg key=value" strings.
type recordingLogger struct {
	debugs []string
}

func (l *recordingLogger) Info(msg string, args ...any) {}
func (l *recordingLogger) Warn(msg string, args ...any) {}
func (l *recordingLogger) Debug(msg string, args ...any) {
	l.debugs = append(l.debugs, formatLogLine(msg, args...))
}

// formatLogLine renders a structured log call the way the tests assert on it.
func formatLogLine(msg string, args ...any) string {
	var b strings.Builder
	b.WriteString(msg)
	for i := 0; i+1 < len(args); i += 2 {
		fmt.Fprintf(&b, " %v=%v", args[i], args[i+1])
	}
	return b.String()
}

func (l *recordingLogger) joined() string { return strings.Join(l.debugs, "\n") }

// stubMessage implements pahomqtt.Message for inbound-dispatch tests.
type stubMessage struct {
	topic   string
	payload []byte
}

func (m stubMessage) Duplicate() bool   { return false }
func (m stubMessage) Qos() byte         { return 1 }
func (m stubMessage) Retained() bool    { return false }
func (m stubMessage) Topic() string     { return m.topic }
func (m stubMessage) MessageID() uint16 { return 1 }
func (m stubMessage) Payload() []byte   { return m.payload }
func (m stubMessage) Ack()              {}

func newTestClient(log *recordingLogger) *MQTTClient {
	return NewMQTTClient(MQTTOptions{Host: "broker.invalid.test", Port: 1883}, log)
}

// TestPublishLogsOutgoing asserts every publish is logged at debug with topic,
// payload and retain flag — including when the publish itself fails (here the
// client was never connected, so paho returns ErrNotConnected immediately).
func TestPublishLogsOutgoing(t *testing.T) {
	log := &recordingLogger{}
	c := newTestClient(log)

	if err := c.Publish("eebus/dev/sensor/power_w/state", false, []byte("1234.5")); err == nil {
		t.Fatal("expected an error from a never-connected client")
	}
	want := formatLogLine("mqtt publish", "topic", "eebus/dev/sensor/power_w/state",
		"payload", "1234.5", "retain", false)
	if len(log.debugs) != 1 || log.debugs[0] != want {
		t.Fatalf("outgoing publish not logged as expected\n got: %q\nwant: %q", log.joined(), want)
	}
}

// TestAdaptHandlerLogsIncoming asserts inbound messages are logged at debug
// before being dispatched to the bridge handler.
func TestAdaptHandlerLogsIncoming(t *testing.T) {
	log := &recordingLogger{}
	c := newTestClient(log)

	got := ""
	h := c.adaptHandler(func(m MQTTMessage) { got = string(m.Payload()) })
	h(nil, stubMessage{topic: "eebus/dev/ohpcf/btn/pause/cmd", payload: []byte("PRESS")})

	if got != "PRESS" {
		t.Fatalf("handler not dispatched, got payload %q", got)
	}
	want := formatLogLine("mqtt recv", "topic", "eebus/dev/ohpcf/btn/pause/cmd", "payload", "PRESS")
	if len(log.debugs) != 1 || log.debugs[0] != want {
		t.Fatalf("incoming message not logged as expected\n got: %q\nwant: %q", log.joined(), want)
	}
}

// TestSubscribeLogsTopic asserts subscription registration is logged at debug.
// The subscribe itself fails (never-connected client) but must still be logged.
func TestSubscribeLogsTopic(t *testing.T) {
	log := &recordingLogger{}
	c := newTestClient(log)

	if err := c.Subscribe("eebus/dev/ohpcf/btn/pause/cmd", func(MQTTMessage) {}); err == nil {
		t.Fatal("expected an error from a never-connected client")
	}
	want := formatLogLine("mqtt subscribe", "topic", "eebus/dev/ohpcf/btn/pause/cmd")
	if len(log.debugs) != 1 || log.debugs[0] != want {
		t.Fatalf("subscription not logged as expected\n got: %q\nwant: %q", log.joined(), want)
	}
}
