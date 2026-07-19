// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Module: eebus-bridge — MQTT/HA bridge consuming eebusd's NDJSON stream.

module eebus-bridge

go 1.24.1

require github.com/eclipse/paho.mqtt.golang v1.5.0

require (
	github.com/gorilla/websocket v1.5.3 // indirect
	golang.org/x/net v0.27.0 // indirect
	golang.org/x/sync v0.7.0 // indirect
)
