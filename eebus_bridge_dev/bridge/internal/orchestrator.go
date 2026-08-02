// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package internal: orchestrator.go — the bridge runtime.
//
// Orchestrates: MQTT connection → eebusd subprocess → NDJSON parser →
// discovery mapper → MQTT publish. Owns the top-level context that ties
// shutdown (SIGTERM) to every component.

package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Orchestrator is the running bridge. Construct one with NewOrchestrator and
// call Run to block until the add-on is asked to stop.
type Orchestrator struct {
	cfg    Config
	logger Logger
}

// NewOrchestrator wires the configuration and logger. Run starts everything.
func NewOrchestrator(cfg Config, logger Logger) *Orchestrator {
	return &Orchestrator{cfg: cfg, logger: logger}
}

// Run starts MQTT, launches eebusd, parses its NDJSON and publishes discovery
// + state until ctx is cancelled or a fatal error occurs. It also installs
// the SIGTERM handler so HA's stop propagates cleanly.
func (o *Orchestrator) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Install signal handler. HA sends SIGTERM to stop add-ons.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sigCh
		o.logger.Info("received signal, shutting down", "signal", s.String())
		cancel()
	}()

	// 1. MQTT connection (with bounded retry).
	mqtt, err := o.connectMQTT(ctx)
	if err != nil {
		return fmt.Errorf("mqtt: %w", err)
	}
	defer mqtt.Disconnect(o.lwtTopic(), o.lwtOnline())

	// 2. EEBUS → HA mapper. Pure logic, no I/O.
	mapper := NewMapper(o.cfg.MQTTPrefix, o.cfg.MQTTDiscovery)

	// 3. Launch eebusd. Its stdout becomes the parser's input.
	eebusd := NewSubprocess(o.cfg.ScannerBin, o.cfg.Args(), o.logger)

	// onStdout is invoked once per (re)start of eebusd. Each call sets up a
	// fresh parser and feeds events into the mapper/publisher/command-router.
	onStdout := func(r io.Reader) {
		parser := NewParser(r, o.logger)
		go func() {
			err := parser.Stream(func(ev Event) {
				o.handleEvent(ev, mapper, mqtt, eebusd)
			})
			if err != nil {
				o.logger.Warn("ndjson parser ended", "err", err.Error())
			}
		}()
	}

	// 4. Block until shutdown or fatal subprocess error.
	const maxRestarts = 3
	if err := eebusd.Run(ctx, maxRestarts, onStdout); err != nil {
		return fmt.Errorf("eebusd: %w", err)
	}
	return nil
}

// connectMQTT retries the connection for up to 30s before giving up.
func (o *Orchestrator) connectMQTT(ctx context.Context) (*MQTTClient, error) {
	client := NewMQTTClient(MQTTOptions{
		Host:        o.cfg.MQTTHost,
		Port:        o.cfg.MQTTPort,
		User:        o.cfg.MQTTUser,
		Password:    o.cfg.MQTTPassword,
		ClientID:    "eebus-bridge",
		WillTopic:   o.statusTopic(),
		WillOnline:  `{"state":"online"}`,
		WillOffline: `{"state":"offline"}`,
	}, o.logger)

	deadline := time.Now().Add(30 * time.Second)
	for {
		connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := client.Connect(connCtx)
		cancel()
		if err == nil {
			return client, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, fmt.Errorf("broker unreachable after 30s: %w", err)
		}
		o.logger.Warn("mqtt connect retry", "err", err.Error())
		time.Sleep(2 * time.Second)
	}
}

// handleEvent routes one parsed NDJSON event to discovery publishing and, for
// write-channel events, to MQTT subscription / command routing.
func (o *Orchestrator) handleEvent(ev Event, mapper *Mapper, mqtt *MQTTClient, eebusd *Subprocess) {
	switch {
	case ev.Manufacturer != nil:
		// Update device registry. No MQTT publish here — the device block is
		// carried by each sensor discovery message, so updating it in the
		// mapper is enough for the next sensor to carry the new name.
		_ = mapper.OnManufacturer(ev.Manufacturer)

	case ev.Measurement != nil:
		disc := mapper.OnMeasurement(ev.Measurement)
		if disc.Config != nil {
			o.publishDiscovery(mqtt, disc)
		}
		o.publishState(mqtt, disc.StateTopic, disc.StateValue)

	case ev.Controllable != nil:
		// A remote entity just announced support for a write use case (OHPCF,
		// LPC, …), or — on a subsequent line — refreshed the controllable's
		// state (e.g. the OHPCF compressor transitioned running → paused).
		// The first call publishes discovery + subscribes to command topics;
		// every call (including refreshes) republishes the state/action so
		// the climate entity's action topic tracks the device in real time.
		disc := mapper.OnControllable(ev.Controllable)
		if disc.Config != nil {
			o.publishDiscovery(mqtt, disc)
			// Subscribe to command topics. Use a closure capturing the
			// controllable context so the inbound handler can build a Command
			// and route it to eebusd's stdin.
			c := ev.Controllable
			for _, topic := range disc.CommandTopics {
				cmdTopic := topic
				o.subscribeCommand(mqtt, cmdTopic, c, eebusd)
			}
		}
		// Always refresh state (and action, when present). On first announce
		// this seeds HA so it does not show "unknown"; on refreshes this
		// propagates device-driven transitions. publishState is a no-op when
		// the topic or value is empty, so climate/number components are both
		// handled uniformly here.
		o.publishState(mqtt, disc.StateTopic, disc.StateValue)
		if disc.ActionTopic != "" {
			o.publishState(mqtt, disc.ActionTopic, disc.ActionValue)
		}

	case ev.UcSignal != nil:
		// A use case read signal (e.g. OHPCF requested power estimate) arrived.
		// Map it to a typed sensor: publish discovery the first time, then only
		// refresh the state on subsequent lines. Read-only: no command topic,
		// so no eebusd write is ever triggered by these entities.
		disc := mapper.OnUcSignal(ev.UcSignal)
		if disc.Config != nil {
			o.publishDiscovery(mqtt, disc)
		}
		o.publishState(mqtt, disc.StateTopic, disc.StateValue)

	case ev.CommandResult != nil:
		// Outcome of a previously-dispatched command. Surface on the bridge
		// status topic and log it; HA already reflects the new state via the
		// next controllable/state line eebusd emits.
		o.logger.Debug("command result", "op", ev.CommandResult.Op,
			"status", ev.CommandResult.Status, "err", ev.CommandResult.Error)
		statusTopic := fmt.Sprintf("%s/bridge/command_result", o.cfg.MQTTPrefix)
		payload := fmt.Sprintf(`{"op":%q,"status":%q}`, ev.CommandResult.Op, ev.CommandResult.Status)
		_ = mqtt.Publish(statusTopic, false, []byte(payload))

	case ev.Configuration != nil:
		// Configuration exposure (setpoints, nameplate values) is still
		// read-only here. Write-side configuration will arrive with the LPC
		// lot. Log at debug so the operator can see the flow.
		o.logger.Debug("configuration event", "ski", ev.Configuration.SKI,
			"key", ev.Configuration.KeyName, "value", ev.Configuration.Value)

	case ev.Device != nil:
		o.logger.Debug("device event", "ski", ev.Device.SKI,
			"entity", ev.Device.Entity, "type", ev.Device.EntityType)

	case ev.Diagnosis != nil:
		o.logger.Debug("diagnosis event", "ski", ev.Diagnosis.SKI,
			"state", ev.Diagnosis.OperatingState)
	}
}

// subscribeCommand registers an MQTT handler on cmdTopic that translates the
// inbound HA payload into an NDJSON Command line and forwards it to eebusd's
// stdin via WriteStdin. The controllable context (c) carries the SKI/entity/
// use case needed to build the op.
//
// The topic suffix decides which action is requested: a "mode/cmd" topic
// carrying "off" maps to <uc>.abort, "auto" to <uc>.schedule; a "preset/cmd"
// topic carrying "pause"/"resume" maps to <uc>.pause/<uc>.resume.
func (o *Orchestrator) subscribeCommand(mqtt *MQTTClient, cmdTopic string, c *Controllable, eebusd *Subprocess) {
	handler := func(payload string) {
		op, value, unit, ok := decodeHACommand(cmdTopic, payload, c)
		if !ok {
			o.logger.Warn("unhandled HA command", "topic", cmdTopic, "payload", payload)
			return
		}
		cmd := Command{
			Kind:   KindCommand,
			Op:     op,
			SKI:    c.SKI,
			Entity: c.Entity,
			Value:  value,
			Unit:   unit,
		}
		line, err := json.Marshal(cmd)
		if err != nil {
			o.logger.Warn("encode command", "err", err.Error())
			return
		}
		if err := eebusd.WriteStdin(string(line)); err != nil {
			o.logger.Warn("write command to eebusd", "err", err.Error())
		}
	}
	// paho delivers a pahomqtt.Message; wrap to extract the payload string.
	if err := mqtt.Subscribe(cmdTopic, func(msg MQTTMessage) {
		handler(string(msg.Payload()))
	}); err != nil {
		o.logger.Warn("subscribe command topic", "topic", cmdTopic, "err", err.Error())
	}
}

// decodeHACommand maps an HA command_topic payload into a (op, value, unit)
// triple for the NDJSON Command wire format. Returns ok=false for payloads we
// do not know how to translate (the orchestrator logs and drops them).
//
// Routing is based on the topic suffix:
//   - /mode/cmd   → off→<uc>.abort, auto/heat/cool→<uc>.schedule (climate)
//   - /preset/cmd → pause→<uc>.pause, resume/none/""→<uc>.resume (climate)
//   - /value/cmd  → <uc>.set with the parsed float value (number entities)
//
// The same decoder works for any use case regardless of component.
func decodeHACommand(topic, payload string, c *Controllable) (op string, value float64, unit string, ok bool) {
	p := strings.TrimSpace(strings.ToLower(payload))
	switch {
	case strings.HasSuffix(topic, "/mode/cmd"):
		switch p {
		case "off":
			return c.UseCase + ".abort", 0, "", true
		case "auto", "heat", "cool":
			return c.UseCase + ".schedule", 0, "seconds", true
		}
	case strings.HasSuffix(topic, "/preset/cmd"):
		switch p {
		case "pause":
			return c.UseCase + ".pause", 0, "", true
		case "resume", "none", "":
			return c.UseCase + ".resume", 0, "", true
		}
	case strings.HasSuffix(topic, "/value/cmd"):
		// Number entities carry a numeric payload (e.g. a watts limit). Parse
		// the trimmed original payload (NOT the lowercased one — parsing digits
		// is case-insensitive anyway, but we avoid any future locale surprise).
		// An empty payload clears the limit.
		raw := strings.TrimSpace(payload)
		if raw == "" {
			return c.UseCase + ".clear", 0, c.Unit, true
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return "", 0, "", false
		}
		return c.UseCase + ".set", v, c.Unit, true
	}
	return "", 0, "", false
}

// publishDiscovery emits the HA discovery config message (retained).
func (o *Orchestrator) publishDiscovery(mqtt *MQTTClient, disc Discovery) {
	payload, err := json.Marshal(disc.Config)
	if err != nil {
		o.logger.Warn("discovery marshal failed", "err", err.Error())
		return
	}
	if err := mqtt.Publish(disc.ConfigTopic, true, payload); err != nil {
		o.logger.Warn("discovery publish failed", "topic", disc.ConfigTopic, "err", err.Error())
	}
}

// publishState emits a sensor state value.
func (o *Orchestrator) publishState(mqtt *MQTTClient, topic, value string) {
	if topic == "" || value == "" {
		return
	}
	if err := mqtt.Publish(topic, false, []byte(value)); err != nil {
		o.logger.Warn("state publish failed", "topic", topic, "err", err.Error())
	}
}

// ---- topic helpers ---------------------------------------------------------

func (o *Orchestrator) statusTopic() string {
	return fmt.Sprintf("%s/bridge/status", o.cfg.MQTTPrefix)
}

func (o *Orchestrator) lwtTopic() string  { return o.statusTopic() }
func (o *Orchestrator) lwtOnline() string { return `{"state":"online"}` }
