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
	// fresh parser and feeds events into the mapper/publisher.
	onStdout := func(r io.Reader) {
		parser := NewParser(r, o.logger)
		go func() {
			err := parser.Stream(func(ev Event) {
				o.handleEvent(ev, mapper, mqtt)
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

// handleEvent routes one parsed NDJSON event to discovery publishing.
func (o *Orchestrator) handleEvent(ev Event, mapper *Mapper, mqtt *MQTTClient) {
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

	case ev.Configuration != nil:
		// Configuration + diagnosis exposure will be expanded in the write
		// jalot. For now, log at debug so the operator can see the flow.
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
