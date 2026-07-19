// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package internal: logger.go — structured logging with secret redaction.
//
// The bridge handles MQTT credentials and a SHIP pairing secret in memory.
// This logger wraps log/slog and redacts any value whose key matches a known
// secret pattern, so an accidental `logger.Info("config", "mqtt_password", x)`
// cannot leak a credential to the add-on logs.

package internal

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// secretKeyFragments are case-insensitive substrings that mark an attribute
// key as sensitive. If any of them appears in the key, the value is replaced
// with "<redacted>" in the log output.
var secretKeyFragments = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"apikey",
	"api_key",
	"credential",
}

// redactingHandler wraps a slog.Handler so that any attribute whose key looks
// sensitive has its value replaced before it reaches the underlying handler.
type redactingHandler struct {
	inner slog.Handler
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Copy + redact before forwarding so the wrapped handler never sees secrets.
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		if isSecretKey(a.Key) {
			a.Value = slog.StringValue("<redacted>")
		}
		out[i] = a
	}
	return &redactingHandler{inner: h.inner.WithAttrs(out)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{inner: h.inner.WithGroup(name)}
}

func (h *redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	// Clone the record and redact in-place; we cannot mutate the original
	// safely, so we build a new record with the same level/message/time.
	clone := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		if isSecretKey(a.Key) {
			a.Value = slog.StringValue("<redacted>")
		}
		clone.AddAttrs(a)
		return true
	})
	return h.inner.Handle(ctx, clone)
}

func isSecretKey(key string) bool {
	k := strings.ToLower(key)
	for _, frag := range secretKeyFragments {
		if strings.Contains(k, frag) {
			return true
		}
	}
	return false
}

// NewLogger returns a slog.Logger at the given level that writes JSON to
// stdout (Home Assistant captures add-on stdout) and redacts secret values.
//
// level is one of: "trace" (debug), "debug", "info", "warning", "error".
// Unknown values fall back to "info".
func NewLogger(level string) *slog.Logger {
	lvl := slog.LevelInfo
	switch strings.ToLower(level) {
	case "trace", "debug":
		lvl = slog.LevelDebug
	case "warning", "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	inner := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(&redactingHandler{inner: inner})
}
