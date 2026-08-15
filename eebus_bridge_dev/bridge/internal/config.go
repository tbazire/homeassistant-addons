// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package internal: config.go — environment-driven configuration.
//
// The bridge does NOT parse the HA options.json itself; run.sh (bashio) does
// that and exports each option as an EEBUS_* environment variable. This keeps
// the bridge free of HA-specific dependencies and easy to test by setting env
// vars directly.
//
// Every accessor returns a typed value with a sensible default. Missing env
// vars are normal (most options are optional); invalid values cause Config to
// refuse to build.

package internal

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds every knob the bridge needs at runtime.
type Config struct {
	LogLevel     string // trace|debug|info|warning|error
	PollInterval int    // seconds; 0 = subscription-only

	// Pairing (forwarded to eebusd).
	AutoAccept bool
	RemoteSKI  string // empty = auto-discovery
	Secret     string // empty = no listener pairing mode

	// eebusd identity (forwarded to eebusd).
	Brand  string
	Model  string
	Serial string
	Vendor string
	Port   int

	// MQTT. TLS switches the broker connection from tcp:// to ssl:// (system
	// CA pool) for external brokers that require it (port 8883, cloud brokers).
	MQTTHost      string
	MQTTPort      int
	MQTTUser      string
	MQTTPassword  string
	MQTTTLS       bool
	MQTTPrefix    string
	MQTTDiscovery string

	// Write commands (forwarded to eebusd). When Enable is true, the bridge
	// passes -commands to eebusd, opens the stdin backchannel, subscribes to
	// HA command topics, and routes inbound commands to eebusd. Default
	// false: the add-on is read-only.
	WriteEnable        bool
	WriteUseCases      string // "auto" (default) or comma-separated list
	WriteDeviceProfile string // "auto" (default) or heatpump|evse|inverter|battery|generic
	WriteLPCMaxLimitW  int    // fallback max (W) for the LPC number when nominal_max is absent; 0 = eebusd default
	// Per-use-case security toggles (opt-in). Forwarded to eebusd as
	// -write-lpc-enabled / -write-ohpcf-enabled. A use case is active only when
	// both WriteEnable and its own toggle are true.
	WriteLPCEnabled   bool
	WriteOHPCFEnabled bool

	// Process wiring (set by run.sh).
	ScannerBin string // path to eebusd binary
	DataDir    string // persistent dir for eebusd certs + ring buffer
}

// Load reads the EEBUS_* environment variables and returns a populated Config.
// Returns an error if a required value is missing or malformed.
func Load() (Config, error) {
	cfg := Config{
		LogLevel:           envDefault("EEBUS_LOG_LEVEL", "info"),
		PollInterval:       envInt("EEBUS_POLL_INTERVAL", 60),
		AutoAccept:         envBool("EEBUS_PAIRING_AUTO_ACCEPT", false),
		RemoteSKI:          os.Getenv("EEBUS_PAIRING_REMOTE_SKI"),
		Secret:             os.Getenv("EEBUS_PAIRING_SECRET"),
		Brand:              envDefault("EEBUS_SCANNER_BRAND", "EEBusBridge"),
		Model:              envDefault("EEBUS_SCANNER_MODEL", "Bridge-1"),
		Serial:             envDefault("EEBUS_SCANNER_SERIAL", "bridge-0001"),
		Vendor:             envDefault("EEBUS_SCANNER_VENDOR", "EBRG"),
		Port:               envInt("EEBUS_SCANNER_PORT", 4711),
		MQTTHost:           os.Getenv("EEBUS_MQTT_HOST"),
		MQTTPort:           envInt("EEBUS_MQTT_PORT", 1883),
		MQTTUser:           os.Getenv("EEBUS_MQTT_USER"),
		MQTTPassword:       os.Getenv("EEBUS_MQTT_PASSWORD"),
		MQTTTLS:            envBool("EEBUS_MQTT_SSL", false),
		MQTTPrefix:         envDefault("EEBUS_MQTT_PREFIX", "eebus"),
		MQTTDiscovery:      envDefault("EEBUS_MQTT_DISCOVERY", "homeassistant"),
		WriteEnable:        envBool("EEBUS_WRITE_ENABLE", false),
		WriteUseCases:      envDefault("EEBUS_WRITE_USE_CASES", "auto"),
		WriteDeviceProfile: envDefault("EEBUS_WRITE_DEVICE_PROFILE", "auto"),
		WriteLPCMaxLimitW:  envInt("EEBUS_WRITE_LPC_MAX_LIMIT_W", 0),
		WriteLPCEnabled:    envBool("EEBUS_WRITE_LPC_ENABLED", false),
		WriteOHPCFEnabled:  envBool("EEBUS_WRITE_OHPCF_ENABLED", false),
		ScannerBin:         envDefault("EEBUS_SCANNER_BIN", "/usr/local/bin/eebusd"),
		DataDir:            envDefault("EEBUS_DATA_DIR", "/data/eebus"),
	}

	if cfg.MQTTHost == "" {
		return cfg, errors.New("EEBUS_MQTT_HOST is required (run.sh must resolve the broker)")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return cfg, fmt.Errorf("invalid scanner port %d", cfg.Port)
	}
	if cfg.MQTTPort < 1 || cfg.MQTTPort > 65535 {
		return cfg, fmt.Errorf("invalid MQTT port %d", cfg.MQTTPort)
	}
	if cfg.RemoteSKI != "" && !isHexSKI(cfg.RemoteSKI) {
		return cfg, fmt.Errorf("remote_ski must be 40 hex chars, got %d", len(cfg.RemoteSKI))
	}
	return cfg, nil
}

// Args builds the CLI flags to pass to eebusd. The bridge always runs eebusd
// in -json mode (its output is the NDJSON stream we consume); logs go to
// stderr so they do not corrupt stdout.
func (c Config) Args() []string {
	args := []string{
		"-json",
		"-loglevel", c.LogLevel,
		"-poll-interval", fmt.Sprintf("%ds", c.PollInterval),
		"-brand", c.Brand,
		"-model", c.Model,
		"-serial", c.Serial,
		"-vendor", c.Vendor,
		"-port", strconv.Itoa(c.Port),
		"-certdir", c.DataDir,
	}
	if c.AutoAccept {
		args = append(args, "-autoaccept")
	}
	if c.RemoteSKI != "" {
		args = append(args, "-remoteski", c.RemoteSKI)
	}
	if c.Secret != "" {
		args = append(args, "-secret", c.Secret)
	}
	if c.WriteEnable {
		// Enable the write backchannel: eebusd reads NDJSON commands on stdin
		// and registers its write use cases (OHPCF, …). The two advisory flags
		// let the user restrict which use cases activate and hint the device
		// profile so the discovery is filtered (useful when several devices
		// are paired but only one should be driven).
		args = append(args, "-commands")
		args = append(args, "-write-usecases", c.WriteUseCases)
		args = append(args, "-write-profile", c.WriteDeviceProfile)
		// Fallback max for the LPC number entity when the device does not expose
		// a nominal max. eebusd applies its own default when 0.
		args = append(args, "-write-lpc-max-limit-w", strconv.Itoa(c.WriteLPCMaxLimitW))
		// Per-use-case security toggles (opt-in). A use case is active only when
		// both -commands and its own flag are set.
		//
		// IMPORTANT: bool flags MUST be emitted as "-flag=value" (single token),
		// NOT "-flag value" (two tokens). Go's flag package treats a space-
		// separated value for a bool flag as a positional argument, which stops
		// flag parsing at that point — so any flag after the first bool would be
		// silently dropped. This is exactly the bug that shipped in 0.6.7:
		// OHPCFEnabled was never parsed because -write-ohpcf-enabled came after
		// the space-separated -write-lpc-enabled value, so OHPCF never bound and
		// its buttons never appeared in Home Assistant.
		args = append(args, "-write-lpc-enabled="+strconv.FormatBool(c.WriteLPCEnabled))
		args = append(args, "-write-ohpcf-enabled="+strconv.FormatBool(c.WriteOHPCFEnabled))
	}
	return args
}

// Redacted returns a copy of the config safe for logging: secrets are masked.
// Used at startup so the operator can confirm the effective config without
// leaking credentials.
func (c Config) Redacted() map[string]any {
	mask := func(s string) string {
		if s == "" {
			return ""
		}
		return "<set>"
	}
	return map[string]any{
		"log_level":             c.LogLevel,
		"poll_interval":         c.PollInterval,
		"auto_accept":           c.AutoAccept,
		"remote_ski":            c.RemoteSKI,
		"secret":                mask(c.Secret),
		"brand":                 c.Brand,
		"model":                 c.Model,
		"serial":                c.Serial,
		"vendor":                c.Vendor,
		"port":                  c.Port,
		"mqtt_host":             c.MQTTHost,
		"mqtt_port":             c.MQTTPort,
		"mqtt_user":             mask(c.MQTTUser),
		"mqtt_password":         mask(c.MQTTPassword),
		"mqtt_ssl":              c.MQTTTLS,
		"mqtt_prefix":           c.MQTTPrefix,
		"discovery":             c.MQTTDiscovery,
		"write_enable":          c.WriteEnable,
		"write_use_cases":       c.WriteUseCases,
		"write_device_profile":  c.WriteDeviceProfile,
		"write_lpc_max_limit_w": c.WriteLPCMaxLimitW,
		"write_lpc_enabled":     c.WriteLPCEnabled,
		"write_ohpcf_enabled":   c.WriteOHPCFEnabled,
		"scanner_bin":           c.ScannerBin,
		"data_dir":              c.DataDir,
	}
}

// ---- env helpers -----------------------------------------------------------

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

func envBool(key string, def bool) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// isHexSKI returns true iff s is exactly 40 hexadecimal characters.
func isHexSKI(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
