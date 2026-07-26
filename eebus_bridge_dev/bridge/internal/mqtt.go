// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package internal: mqtt.go — MQTT transport layer.
//
// Single responsibility: manage the connection lifecycle (connect, reconnect,
// LWT) and expose a tiny Publish/Subscribe API. No EEBUS or HA knowledge here
// — those concerns live in discovery.go and the orchestrator.
//
// Uses paho.mqtt.golang (pure Go, CGO-free).

package internal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// MQTTMessage is the small surface of a paho message the rest of the bridge
// needs. Declared as an interface so callers do not depend on paho types.
type MQTTMessage interface {
	Topic() string
	Payload() []byte
}

// MessageHandler is the bridge-internal signature for inbound MQTT messages.
// It decouples the orchestrator from paho's MessageHandler type.
type MessageHandler func(MQTTMessage)

// MQTTClient wraps paho's client with a connect-with-retry helper and an
// idiomatic Go API. It deliberately hides paho's option sprawl behind sane
// defaults (QoS 1, auto-reconnect, 10s connection timeout, LWT "offline").
type MQTTClient struct {
	client pahomqtt.Client
	logger Logger

	mu   sync.Mutex
	subs map[string]MessageHandler // active subscriptions, re-applied on reconnect
}

// MQTTOptions are the values the bridge actually needs to configure.
type MQTTOptions struct {
	Host        string
	Port        int
	User        string
	Password    string
	ClientID    string
	WillTopic   string // LWT topic (empty = no LWT)
	WillOnline  string // payload published as a retained "online" when connected
	WillOffline string // payload published by the broker if we disconnect ungracefully
}

// NewMQTTClient constructs a client (does not connect yet).
func NewMQTTClient(opts MQTTOptions, logger Logger) *MQTTClient {
	if opts.ClientID == "" {
		opts.ClientID = "eebus-bridge"
	}
	broker := fmt.Sprintf("tcp://%s:%d", opts.Host, opts.Port)

	// The *MQTTClient must exist before the OnConnect closure can reference it
	// (the closure re-applies subscriptions held on the wrapper). We create
	// the wrapper with a zeroed paho client, build the options referencing it,
	// then bind the real paho client.
	mc := &MQTTClient{
		logger: logger,
		subs:   make(map[string]MessageHandler),
	}

	pahoOpts := pahomqtt.NewClientOptions().
		AddBroker(broker).
		SetClientID(opts.ClientID).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetPingTimeout(10 * time.Second).
		SetOnConnectHandler(func(c pahomqtt.Client) {
			logger.Info("mqtt connected", "broker", broker)
			// Re-publish LWT "online" so any retained "offline" is cleared.
			if opts.WillTopic != "" && opts.WillOnline != "" {
				c.Publish(opts.WillTopic, 1, true, opts.WillOnline)
			}
			// Re-apply every active subscription. paho does NOT remember
			// subscriptions across reconnects on its own when auto-reconnect
			// is enabled, so a command_topic we subscribed to before a network
			// blip would silently stop firing without this loop.
			mc.resubscribeAll()
		}).
		SetConnectionLostHandler(func(c pahomqtt.Client, err error) {
			logger.Warn("mqtt connection lost", "err", err.Error())
		})

	if opts.User != "" {
		pahoOpts.SetUsername(opts.User)
		pahoOpts.SetPassword(opts.Password)
	}
	if opts.WillTopic != "" {
		// LWT: broker publishes opts.WillOffline on opts.WillTopic (retained)
		// if we drop. On graceful disconnect we publish opts.WillOnline first.
		pahoOpts.SetWill(opts.WillTopic, opts.WillOffline, 1, true)
	}

	mc.client = pahomqtt.NewClient(pahoOpts)
	return mc
}

// resubscribeAll re-applies every registered subscription to the paho client.
// Called from the OnConnect handler so command topics survive reconnects.
func (c *MQTTClient) resubscribeAll() {
	c.mu.Lock()
	topics := make([]string, 0, len(c.subs))
	for topic := range c.subs {
		topics = append(topics, topic)
	}
	c.mu.Unlock()
	for _, topic := range topics {
		c.mu.Lock()
		h := c.subs[topic]
		c.mu.Unlock()
		if h == nil {
			continue
		}
		if tkn := c.client.Subscribe(topic, 1, adaptHandler(h)); tkn.WaitTimeout(5 * time.Second) {
			if err := tkn.Error(); err != nil {
				c.logger.Warn("mqtt re-subscribe failed", "topic", topic, "err", err.Error())
			}
		}
	}
}

// adaptHandler converts a bridge-internal MessageHandler into the paho
// MessageHandler signature, wrapping the paho message behind MQTTMessage.
func adaptHandler(h MessageHandler) pahomqtt.MessageHandler {
	return func(_ pahomqtt.Client, m pahomqtt.Message) {
		h(pahoMessage{m})
	}
}

// pahoMessage adapts a paho Message to the MQTTMessage interface.
type pahoMessage struct{ pahomqtt.Message }

func (m pahoMessage) Topic() string   { return m.Message.Topic() }
func (m pahoMessage) Payload() []byte { return m.Message.Payload() }

// Connect blocks until the broker is reachable or ctx is cancelled. paho's
// own retry loop takes over for reconnections after the first connect.
func (c *MQTTClient) Connect(ctx context.Context) error {
	token := c.client.Connect()
	// Wait on either the paho token or the context.
	waitCh := make(chan struct{})
	go func() {
		token.Wait()
		close(waitCh)
	}()
	select {
	case <-waitCh:
		if err := token.Error(); err != nil {
			return fmt.Errorf("mqtt connect: %w", err)
		}
		return nil
	case <-ctx.Done():
		return errors.New("mqtt connect cancelled")
	}
}

// Publish sends a message. qos=1, retain follows the argument. Returns an
// error if the publish did not complete within 10s.
func (c *MQTTClient) Publish(topic string, retained bool, payload []byte) error {
	token := c.client.Publish(topic, 1, retained, payload)
	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("mqtt publish timeout: %s", topic)
	}
	return token.Error()
}

// Subscribe registers a handler for a topic. The subscription is tracked so it
// is automatically re-applied on reconnect (paho does not remember subs across
// reconnects when auto-reconnect is enabled).
func (c *MQTTClient) Subscribe(topic string, handler MessageHandler) error {
	c.mu.Lock()
	c.subs[topic] = handler
	c.mu.Unlock()
	token := c.client.Subscribe(topic, 1, adaptHandler(handler))
	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("mqtt subscribe timeout: %s", topic)
	}
	return token.Error()
}

// Disconnect cleanly closes the connection. Before doing so it publishes the
// LWT "online -> offline" transition so subscribers see a graceful state.
func (c *MQTTClient) Disconnect(willTopic, onlinePayload string) {
	if willTopic != "" && onlinePayload != "" && c.client.IsConnected() {
		// Publish offline before disconnecting. The paho LWT only fires on
		// ungraceful loss, so we must clear our own online marker ourselves.
		c.client.Publish(willTopic, 1, true, strings.Replace(onlinePayload, "online", "offline", 1))
	}
	c.client.Disconnect(250) // 250ms grace
}

// IsConnected returns the live connection state.
func (c *MQTTClient) IsConnected() bool { return c.client.IsConnected() }
