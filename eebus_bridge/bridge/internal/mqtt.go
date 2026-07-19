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

// MQTTClient wraps paho's client with a connect-with-retry helper and an
// idiomatic Go API. It deliberately hides paho's option sprawl behind sane
// defaults (QoS 1, auto-reconnect, 10s connection timeout, LWT "offline").
type MQTTClient struct {
	client pahomqtt.Client
	logger Logger

	mu   sync.Mutex
	subs map[string]pahomqtt.MessageHandler // active subscriptions
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

	return &MQTTClient{
		client: pahomqtt.NewClient(pahoOpts),
		logger: logger,
		subs:   make(map[string]pahomqtt.MessageHandler),
	}
}

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

// Subscribe registers a handler for a topic. Re-applied automatically by paho
// on reconnect only if registered through this method (we track them so the
// orchestrator can re-subscribe if needed).
func (c *MQTTClient) Subscribe(topic string, handler pahomqtt.MessageHandler) error {
	c.mu.Lock()
	c.subs[topic] = handler
	c.mu.Unlock()
	token := c.client.Subscribe(topic, 1, handler)
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
